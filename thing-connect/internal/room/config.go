package room

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type Config struct {
	ParticipantLimit  int    `json:"participant_limit" yaml:"participant_limit"`
	EmptyTTL          string `json:"empty_ttl" yaml:"empty_ttl"`
	PresenceHeartbeat string `json:"presence_heartbeat" yaml:"presence_heartbeat"`
	PresenceLease     string `json:"presence_lease" yaml:"presence_lease"`
	CodeCooldown      string `json:"code_cooldown" yaml:"code_cooldown"`
	CommandTTL        string `json:"command_ttl" yaml:"command_ttl"`
	TokenTimeout      string `json:"token_timeout" yaml:"token_timeout"`
}

const DefaultConfigJSON = `{"participant_limit":100,"empty_ttl":"24h","presence_heartbeat":"15s","presence_lease":"45s","code_cooldown":"24h","command_ttl":"20s","token_timeout":"5s"}`

func DefaultConfig() Config {
	var c Config
	_ = json.Unmarshal([]byte(DefaultConfigJSON), &c)
	return c
}
func ParseConfig(raw []byte) (Policy, error) {
	var c Config
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(&c); e != nil {
		return Policy{}, e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return Policy{}, fmt.Errorf("unexpected trailing room policy")
	}
	return c.Policy()
}
func (c Config) Policy() (Policy, error) {
	p := Policy{ParticipantLimit: c.ParticipantLimit}
	for _, field := range []struct {
		text   string
		target *time.Duration
	}{{c.EmptyTTL, &p.EmptyTTL}, {c.PresenceHeartbeat, &p.Heartbeat}, {c.PresenceLease, &p.Lease}, {c.CodeCooldown, &p.CodeCooldown}, {c.CommandTTL, &p.CommandTTL}, {c.TokenTimeout, &p.TokenTimeout}} {
		v, e := time.ParseDuration(field.text)
		if e != nil {
			return p, e
		}
		*field.target = v
	}
	return p, p.Validate()
}
