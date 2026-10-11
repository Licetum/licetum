package model

// LogSettings is the structure of the process logs: which attributes each record keeps and under which names,
// which event categories are written, how the subject is treated and how allowed decisions are sampled. The admin
// API reads and replaces it; every replica applies a change within seconds, without a restart (see logx).
type LogSettings struct {
	Format        string     `json:"format" enum:"json,logfmt,text" doc:"json: one JSON object per line; logfmt: key=value pairs; text: plain lines"`
	Level         string     `json:"level" enum:"debug,info,warn,error" doc:"The lowest level written"`
	Fields        []LogField `json:"fields" nullable:"false" doc:"The attributes to write, each under its name. Empty: every attribute, under its own name"`
	Events        LogEvents  `json:"events" doc:"Switches of the event categories"`
	Subject       string     `json:"subject" enum:"hash,plain,omit" doc:"What the subject of a record becomes: hash (a keyed hash: the same subject gives the same value), plain (as it is) or omit"`
	SampleAllowed float64    `json:"sample_allowed" minimum:"0" maximum:"1" doc:"The share of allowed decisions that are logged, between 0 and 1. Denials are always logged"`
}

// LogField names one attribute the logs keep and the name it is written under.
type LogField struct {
	Name string `json:"name" minLength:"1" maxLength:"64" doc:"The attribute: request_id, method, path, operation, user, status, duration_ms, service, subject, type, resource_id, action, allowed, consistency, or a built-in key (time, level, msg)" example:"subject"`
	As   string `json:"as" required:"false" maxLength:"64" doc:"The name it is written under. Empty: the same name" example:"user"`
}

// LogEvents switches the event categories of the logs.
type LogEvents struct {
	Access   bool `json:"access" doc:"The request line of the API: one per HTTP request"`
	Decision bool `json:"decision" doc:"One line per answer of the check API (allowed answers are sampled, see sample_allowed)"`
	Sync     bool `json:"sync" doc:"A resource synchronization of a type"`
}
