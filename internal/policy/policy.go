package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"slices"

	"go.yaml.in/yaml/v3"
)

type Level string

const (
	None    Level = "none"
	Summary Level = "summary"
	Full    Level = "full"
)

const (
	Allow           = "allowed"
	FixedDeny       = "fixed_category_denied"
	UnknownCategory = "unknown_category"
	MixedCategories = "mixed_categories"
	OperationDenied = "operation_denied"
	ResourceDenied  = "resource_denied"
	DisclosureDeny  = "disclosure_denied"
	UnverifiedInput = "unverified_payload"
)

type Config struct {
	Version   int                         `yaml:"version" json:"version"`
	Data      map[string]Level            `yaml:"data" json:"data"`
	Agents    map[string]map[string]Level `yaml:"agents,omitempty" json:"agents,omitempty"`
	Execution Execution                   `yaml:"execution" json:"execution"`
	Resources []string                    `yaml:"resources" json:"resources"`
}

type Execution struct {
	Consult bool `yaml:"consult" json:"consult"`
	Read    bool `yaml:"read" json:"read"`
}

type Policy struct {
	config   Config
	revision string
}

// Portion describes one independently classified piece of input or output.
// The caller must establish Level from its source, not from a client's label.
type Portion struct {
	Categories []string
	Level      Level
}

type Access struct {
	Agent     string
	Operation string
	Resource  string
	Portions  []Portion
}

type Decision struct {
	Code     string
	Category string
	Level    Level
}

func (d Decision) Allowed() bool { return d.Code == Allow }

var (
	ErrInvalid = errors.New("invalid policy configuration")
	identifier = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

func Load(path string) (*Policy, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrInvalid
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return nil, ErrInvalid
	}
	return Parse(data)
}

func Parse(data []byte) (*Policy, error) {
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, ErrInvalid
	}
	return FromConfig(cfg)
}

func FromConfig(cfg Config) (*Policy, error) {
	if cfg.Version != 1 || cfg.Data == nil {
		return nil, ErrInvalid
	}
	// Clone first so callers cannot modify the loaded policy after validation.
	raw, err := json.Marshal(cfg)
	var cloned Config
	if err != nil || json.Unmarshal(raw, &cloned) != nil {
		return nil, ErrInvalid
	}
	cfg = cloned
	for category, level := range cfg.Data {
		if !identifier.MatchString(category) || !validLevel(level) || (fixed(category) && level != None) {
			return nil, ErrInvalid
		}
	}
	for _, key := range []string{"work", "health"} {
		cfg.Data[key] = None
	}
	for agent, overrides := range cfg.Agents {
		if !identifier.MatchString(agent) {
			return nil, ErrInvalid
		}
		for category, level := range overrides {
			if _, known := cfg.Data[category]; !known || !validLevel(level) || (fixed(category) && level != None) {
				return nil, ErrInvalid
			}
		}
	}
	seen := make(map[string]bool)
	for _, name := range cfg.Resources {
		if !identifier.MatchString(name) || seen[name] {
			return nil, ErrInvalid
		}
		seen[name] = true
	}
	slices.Sort(cfg.Resources)
	canonical, err := json.Marshal(cfg)
	if err != nil {
		return nil, ErrInvalid
	}
	hash := sha256.Sum256(canonical)
	return &Policy{config: cfg, revision: hex.EncodeToString(hash[:])}, nil
}

func (p *Policy) Revision() string { return p.revision }

// NoBroaderThan also checks effective agent overrides: removing an override
// can broaden access when the category default is less restrictive.
func (p *Policy) NoBroaderThan(current *Policy) bool {
	if p.config.Execution.Read && !current.config.Execution.Read || p.config.Execution.Consult && !current.config.Execution.Consult {
		return false
	}
	for _, resource := range p.config.Resources {
		if !hasResource(current.config.Resources, resource) {
			return false
		}
	}
	agents := map[string]bool{"": true}
	for agent := range p.config.Agents {
		agents[agent] = true
	}
	for agent := range current.config.Agents {
		agents[agent] = true
	}
	for category := range p.config.Data {
		for agent := range agents {
			if levelRank(p.limit(agent, category)) > levelRank(current.limit(agent, category)) {
				return false
			}
		}
	}
	return true
}

func levelRank(level Level) int {
	switch level {
	case Full:
		return 2
	case Summary:
		return 1
	default:
		return 0
	}
}

func (p *Policy) limit(agent, category string) Level {
	if override, ok := p.config.Agents[agent][category]; ok {
		return override
	}
	return p.config.Data[category]
}

// Config returns an independent copy suitable for displaying the effective policy.
func (p *Policy) Config() Config {
	raw, _ := json.Marshal(p.config)
	var copy Config
	_ = json.Unmarshal(raw, &copy)
	return copy
}

func (p *Policy) Check(access Access) Decision {
	for _, portion := range access.Portions {
		for _, category := range portion.Categories {
			if fixed(category) {
				return Decision{Code: FixedDeny, Category: category, Level: None}
			}
		}
	}
	if !identifier.MatchString(access.Agent) ||
		!(access.Operation == "consult" && p.config.Execution.Consult || access.Operation == "read" && p.config.Execution.Read) {
		return Decision{Code: OperationDenied}
	}
	if !hasResource(p.config.Resources, access.Resource) {
		return Decision{Code: ResourceDenied}
	}
	if len(access.Portions) == 0 {
		return Decision{Code: UnknownCategory}
	}
	for _, portion := range access.Portions {
		if len(portion.Categories) == 0 {
			return Decision{Code: UnknownCategory}
		}
		for _, category := range portion.Categories {
			if _, known := p.config.Data[category]; !known {
				return Decision{Code: UnknownCategory}
			}
		}
		if len(portion.Categories) != 1 {
			return Decision{Code: MixedCategories}
		}
		category := portion.Categories[0]
		limit := p.limit(access.Agent, category)
		if !validLevel(portion.Level) || portion.Level == None || limit == None || (limit == Summary && portion.Level != Summary) {
			return Decision{Code: DisclosureDeny, Category: category, Level: limit}
		}
	}
	return Decision{Code: Allow}
}

func fixed(category string) bool  { return category == "work" || category == "health" }
func validLevel(level Level) bool { return level == None || level == Summary || level == Full }

func hasResource(list []string, candidate string) bool {
	for _, resource := range list {
		if resource == candidate {
			return true
		}
	}
	return false
}
