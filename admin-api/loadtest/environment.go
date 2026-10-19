package loadtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Environment is what a run was measured on, written to environment.json before the run. A result is compared only with
// results that have the same environment; the report says which environment it describes.
type Environment struct {
	CollectedAt    time.Time         `json:"collected_at"`
	GitCommit      string            `json:"git_commit"`
	GitDirty       bool              `json:"git_dirty"`
	CPUModel       string            `json:"cpu_model"`
	CPUs           int               `json:"logical_cpus"`
	MemoryKiB      int64             `json:"memory_kib"`
	OS             string            `json:"os"`
	Kernel         string            `json:"kernel"`
	CPUGovernor    string            `json:"cpu_governor"`
	LoadAverage    string            `json:"load_average_at_start"`
	GoVersion      string            `json:"go_version"`
	K6Version      string            `json:"k6_version"`
	DockerVersion  string            `json:"docker_version"`
	PostgresVer    string            `json:"postgres_version"`
	PostgresConfig map[string]string `json:"postgres_settings"`
	RedisVersion   string            `json:"redis_version"`
	CheckBinary    string            `json:"check_binary"`
	CheckSHA256    string            `json:"check_binary_sha256"`
	CheckEnv       map[string]string `json:"check_server_env"`
	ProcessEnv     map[string]string `json:"harness_env"`
	K6CPUs         string            `json:"k6_cpus"`
	CheckCPUs      string            `json:"check_cpus"`
	Containers     string            `json:"containers"`
	StackCPUs      map[string]string `json:"stack_cpus"`
	PoolConns      int               `json:"check_db_pool_connections"`
}

// environmentPrefixes are the settings of the product the report records (secrets are masked, see maskSetting).
var environmentPrefixes = []string{"DB__", "REDIS__", "SYNC__", "DELEGATION__", "LOG__", "AUTH__", "HTTP__", "LICETUM_", "GOMAXPROCS", "GOGC", "GOMEMLIMIT"}

// collectEnvironment writes what can be read about the host, the tools and the infrastructure of the run.
func collectEnvironment(ctx context.Context, e *env, opts Options, checkBin string) (Environment, error) {
	env := Environment{
		CollectedAt:    time.Now().UTC(),
		CPUs:           runtime.NumCPU(),
		OS:             readOSRelease(),
		Kernel:         readTrim("/proc/sys/kernel/osrelease"),
		CPUGovernor:    readTrim("/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor"),
		LoadAverage:    readTrim("/proc/loadavg"),
		CPUModel:       readCPUModel(),
		MemoryKiB:      readMemTotal(),
		GoVersion:      runtime.Version(),
		K6Version:      k6Version(opts.K6Bin),
		DockerVersion:  commandOutput(ctx, "docker", "version", "--format", "{{.Server.Version}}"),
		GitCommit:      commandOutput(ctx, "git", "rev-parse", "HEAD"),
		GitDirty:       commandOutput(ctx, "git", "status", "--porcelain") != "",
		K6CPUs:         cpuList(opts.K6CPUs, runtime.NumCPU()),
		CheckCPUs:      cpuList(opts.CheckCPUs, runtime.NumCPU()),
		Containers:     "testcontainers, no CPU or memory limits set by the harness (external servers when named by LICETUM_TEST_*)",
		StackCPUs:      stackCPUs(ctx),
		PoolConns:      opts.DBPool,
		PostgresConfig: map[string]string{},
		CheckEnv:       map[string]string{},
		ProcessEnv:     map[string]string{},
		CheckBinary:    checkBin,
	}
	if raw, err := os.ReadFile(checkBin); err == nil { //nolint:gosec // the binary the run measures
		sum := sha256.Sum256(raw)
		env.CheckSHA256 = hex.EncodeToString(sum[:])
	}
	if err := e.pool.QueryRow(ctx, `SELECT version()`).Scan(&env.PostgresVer); err != nil {
		return env, fmt.Errorf("read the PostgreSQL version: %w", err)
	}
	if err := readPostgresSettings(ctx, e.pool, env.PostgresConfig); err != nil {
		return env, err
	}
	if info, err := e.app.Redis.Info(ctx, "server").Result(); err == nil {
		env.RedisVersion = redisField(info, "redis_version")
	}
	for _, kv := range os.Environ() {
		key, value, _ := strings.Cut(kv, "=")
		if hasAnyPrefix(key, environmentPrefixes) {
			env.ProcessEnv[key] = maskSetting(key, value)
		}
	}
	for _, kv := range checkServerEnv(e, opts) {
		key, value, _ := strings.Cut(kv, "=")
		env.CheckEnv[key] = maskSetting(key, value)
	}
	return env, nil
}

// checkServerEnv is the environment the check server is started with (see startPDP).
func checkServerEnv(e *env, opts Options) []string {
	return []string{
		"DB__URI=" + e.checkDBURI,
		fmt.Sprintf("DB__POOL_SIZE=%d", opts.DBPool),
		"DB__MAX_OVERFLOW=0",
		"REDIS__URI=" + e.checkRedisURI,
		"AUTH__STATE_SECRET=" + e.stateSecret,
	}
}

// maskSetting hides what must not be published: the password of a connection string, and any secret value.
func maskSetting(key, value string) string {
	upper := strings.ToUpper(key)
	if strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "TOKEN") {
		return "***"
	}
	if strings.Contains(value, "://") {
		if u, err := url.Parse(value); err == nil && u.User != nil {
			if _, has := u.User.Password(); has {
				u.User = url.UserPassword(u.User.Username(), "***")
			}
			return u.String()
		}
	}
	return value
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// postgresSettings are the server settings the numbers depend on.
var postgresSettings = []string{
	"shared_buffers", "work_mem", "maintenance_work_mem", "effective_cache_size", "max_connections",
	"synchronous_commit", "max_wal_size", "checkpoint_timeout", "autovacuum", "jit", "random_page_cost",
}

func readPostgresSettings(ctx context.Context, pool *pgxpool.Pool, out map[string]string) error {
	rows, err := pool.Query(ctx, `SELECT name, setting FROM pg_settings WHERE name = ANY($1)`, postgresSettings)
	if err != nil {
		return fmt.Errorf("read the PostgreSQL settings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return fmt.Errorf("scan a PostgreSQL setting: %w", err)
		}
		out[name] = value
	}
	return rows.Err()
}

func redisField(info, field string) string {
	for _, line := range strings.Split(info, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && k == field {
			return v
		}
	}
	return ""
}

// readOSRelease is the pretty name of the operating system.
func readOSRelease() string {
	raw, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return runtime.GOOS
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return runtime.GOOS
}

// readCPUModel is the model name of the first CPU.
func readCPUModel() string {
	raw, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "model name"); ok {
			if _, value, found := strings.Cut(v, ":"); found {
				return strings.TrimSpace(value)
			}
		}
	}
	return "unknown"
}

// readMemTotal is the total memory in KiB.
func readMemTotal() int64 {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			var kib int64
			_, _ = fmt.Sscanf(strings.TrimSpace(v), "%d", &kib)
			return kib
		}
	}
	return 0
}

func readTrim(path string) string {
	raw, err := os.ReadFile(path) //nolint:gosec // the file the environment names
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(raw))
}

// stackCPUs is the CPU set of each container of the test stack (admin-api/loadtest/compose.yml), as docker reports it: "all"
// when the container is not pinned, or "unknown" when docker does not know it.
func stackCPUs(ctx context.Context) map[string]string {
	out := map[string]string{}
	for _, name := range []string{"lt-pg", "lt-redis"} {
		set := commandOutput(ctx, "docker", "inspect", "--format", "{{.HostConfig.CpusetCpus}}", name)
		if set == "" {
			set = "all"
		}
		out[name] = set
	}
	return out
}

// commandOutput is the trimmed output of a command, or "unknown" when it fails.
func commandOutput(ctx context.Context, name string, args ...string) string {
	out, err := exec.CommandContext(ctx, name, args...).Output() //nolint:gosec // fixed tools, fixed arguments
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// cpuList is the CPU set a process is pinned to, or "all" when it is not pinned.
func cpuList(set string, cpus int) string {
	if set == "" {
		return fmt.Sprintf("all (%d)", cpus)
	}
	return set
}
