package redisx

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// Observer is told about every round trip to Redis: a single command (operation is its name in lower case) or a
// pipeline (operation is "pipeline", commands is how many commands travelled together). A pipeline is ONE round trip
// whatever its size, so the count of round trips is what an Observer sums, and the command count is a label.
type Observer interface {
	ObserveRedis(ctx context.Context, operation string, commands int, took time.Duration, err error)
}

// Pipeline is the operation name of a round trip that carried several commands.
const Pipeline = "pipeline"

// Setup is the operation name of the commands the client sends by itself when it opens a connection (HELLO, AUTH, SELECT,
// CLIENT SETNAME, READONLY), alone or as the pipeline go-redis makes of them. They run with the context of whichever request
// needed the connection but are not what the request asked for.
const Setup = "connection_setup"

var setupCommands = map[string]bool{"hello": true, "auth": true, "select": true, "client": true, "readonly": true}

func isSetup(cmds ...redis.Cmder) bool {
	for _, c := range cmds {
		if !setupCommands[strings.ToLower(c.Name())] {
			return false
		}
	}
	return len(cmds) > 0
}

type hook struct{ observer atomic.Pointer[observerBox] }

type observerBox struct{ o Observer }

var hooks sync.Map // *redis.Client -> *hook

// Observe attaches o to rdb: from now on every round trip of the client is reported to it. There is one observer
// per client: a later call replaces the earlier one (the hook is added to the client only once).
func Observe(rdb *redis.Client, o Observer) {
	if rdb == nil {
		return
	}
	h, loaded := hooks.LoadOrStore(rdb, &hook{})
	hk := h.(*hook)
	hk.observer.Store(&observerBox{o: o})
	if !loaded {
		rdb.AddHook(hk)
	}
}

func (h *hook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}

func (h *hook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmd)
		if box := h.observer.Load(); box != nil {
			operation := strings.ToLower(cmd.Name())
			if isSetup(cmd) {
				operation = Setup
			}
			box.o.ObserveRedis(ctx, operation, 1, time.Since(start), failure(err))
		}
		return err
	}
}

func (h *hook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmds)
		if box := h.observer.Load(); box != nil {
			operation := Pipeline
			if isSetup(cmds...) {
				operation = Setup
			}
			box.o.ObserveRedis(ctx, operation, len(cmds), time.Since(start), failure(err))
		}
		return err
	}
}

// failure is err without redis.Nil: a missing key is an answer, not a failure.
func failure(err error) error {
	if errors.Is(err, redis.Nil) {
		return nil
	}
	return err
}
