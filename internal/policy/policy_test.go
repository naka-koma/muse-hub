package policy

import (
	"strings"
	"testing"
)

const sample = `version: 1
data: {code: full, creative: summary, finance: none}
agents:
  reviewer: {code: summary}
execution: {read: true, consult: true}
resources: [arithmetic]
`

func TestStrictPolicy(t *testing.T) {
	for _, raw := range []string{
		"", strings.Replace(sample, "version: 1", "version: 2", 1), sample + "grants: true\n",
		sample + "---\n{}\n", sample + "data: {}\n",
		strings.Replace(sample, "code: full", "health: summary", 1),
		strings.Replace(sample, "code: summary", "work: full", 1),
		strings.Replace(sample, "arithmetic", "../host/path", 1),
		strings.Replace(sample, "consult: true", "shell: true", 1),
	} {
		if _, err := Parse([]byte(raw)); err != ErrInvalid {
			t.Fatalf("invalid policy was accepted: %v", err)
		}
	}
	p, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	copy := p.Config()
	copy.Data["code"] = None
	if p.Config().Data["code"] != Full || p.Config().Data["work"] != None {
		t.Fatal("policy is mutable or the fixed limit is missing")
	}
	same, err := Parse([]byte("# a comment\n" + sample))
	if err != nil || same.Revision() != p.Revision() {
		t.Fatal("equivalent config changed the revision")
	}
}

func TestPolicyOrderAndDisclosure(t *testing.T) {
	p, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	part := func(level Level, categories ...string) Portion { return Portion{Categories: categories, Level: level} }
	for _, row := range []struct {
		agent, operation, resource string
		parts                      []Portion
		want                       string
	}{
		{"dummy", "consult", "arithmetic", []Portion{part(Full, "code")}, Allow},
		{"dummy", "consult", "arithmetic", []Portion{part(Full, "code"), part(Summary, "creative")}, Allow},
		{"dummy", "shell", "outside", []Portion{part(Full, "unknown", "health")}, FixedDeny},
		{"dummy", "write", "arithmetic", []Portion{part(Full, "code")}, OperationDenied},
		{"dummy", "read", "elsewhere", []Portion{part(Full, "code")}, ResourceDenied},
		{"dummy", "read", "arithmetic", []Portion{part(Full, "unknown")}, UnknownCategory},
		{"dummy", "read", "arithmetic", []Portion{part(Full, "code", "creative")}, MixedCategories},
		{"dummy", "read", "arithmetic", []Portion{part(Summary, "finance")}, DisclosureDeny},
		{"reviewer", "consult", "arithmetic", []Portion{part(Full, "code")}, DisclosureDeny},
		{"reviewer", "consult", "arithmetic", []Portion{part(Summary, "code")}, Allow},
	} {
		if got := p.Check(Access{Agent: row.agent, Operation: row.operation, Resource: row.resource, Portions: row.parts}); got.Code != row.want {
			t.Errorf("%v: got %s", row, got.Code)
		}
	}
}
