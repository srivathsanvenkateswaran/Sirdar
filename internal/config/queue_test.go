package config

import (
	"reflect"
	"strings"
	"testing"
)

// trackerWith builds a config whose tracker carries the given queue block,
// so the resolver can be exercised without a file on disk.
func trackerWith(q *QueueConfig) *Config {
	c := &Config{}
	c.Sources.Tracker = &SourceConfig{Adapter: "jira", Queue: q}
	return c
}

func types(v ...string) *[]string {
	out := append([]string(nil), v...)
	return &out
}

func TestQueueTypesDefaultsToBug(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *Config
	}{
		{"no config at all", nil},
		{"no tracker", &Config{}},
		{"no queue block", trackerWith(nil)},
		{"a queue block naming no types", trackerWith(&QueueConfig{})},
	} {
		got := tc.cfg.QueueTypes()
		if !reflect.DeepEqual(got, []string{"bug"}) {
			t.Errorf("%s: QueueTypes = %v, want [bug]", tc.name, got)
		}
		if QueueTypesAll(got) {
			t.Errorf("%s: the default must not mean every type", tc.name)
		}
		if !QueueTypeIsDefault(got) {
			t.Errorf("%s: the default must be recognised as the default", tc.name)
		}
	}
}

func TestQueueTypesExplicit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      []string
		want    []string
		all     bool
		deflt   bool
		matches map[string]bool
	}{
		{
			name: "one type", in: []string{"bug"}, want: []string{"bug"}, deflt: true,
			matches: map[string]bool{"Bug": true, "bug": true, "Defect": true, "Story": false, "": false},
		},
		{
			name: "several, folded and deduped",
			in:   []string{"Bug", "Defect", "Incident", " Task "},
			want: []string{"bug", "incident", "task"},
			matches: map[string]bool{
				"Incident": true, "task": true, "sub-task": false, "": false,
			},
		},
		{
			name: "explicitly empty means every type",
			in:   []string{}, want: []string{}, all: true,
			matches: map[string]bool{"Bug": true, "Sub-task": true, "": true},
		},
		{
			name: "the wildcard means every type",
			in:   []string{"*"}, want: []string{"*"}, all: true,
			matches: map[string]bool{"Bug": true, "anything": true, "": true},
		},
		{
			name: "a wildcard beside a name still means every type",
			in:   []string{"bug", "*"}, want: []string{"bug", "*"}, all: true,
			matches: map[string]bool{"Story": true, "": true},
		},
		{
			name: "a list of blanks is an empty list",
			in:   []string{"", "   "}, want: []string{}, all: true,
			matches: map[string]bool{"": true},
		},
		{
			name: "a type the folding does not know is kept as written",
			in:   []string{"Change Request"}, want: []string{"change request"},
			matches: map[string]bool{"change request": true, "CHANGE REQUEST": true, "bug": false},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := trackerWith(&QueueConfig{Types: types(tc.in...)}).QueueTypes()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("QueueTypes = %v, want %v", got, tc.want)
			}
			if QueueTypesAll(got) != tc.all {
				t.Errorf("QueueTypesAll = %v, want %v", QueueTypesAll(got), tc.all)
			}
			if QueueTypeIsDefault(got) != tc.deflt {
				t.Errorf("QueueTypeIsDefault = %v, want %v", QueueTypeIsDefault(got), tc.deflt)
			}
			for in, want := range tc.matches {
				if MatchQueueType(got, in) != want {
					t.Errorf("MatchQueueType(%v, %q) = %v, want %v", got, in, !want, want)
				}
			}
		})
	}
}

func TestQueueTypesParseFromYAML(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want []string
	}{
		{"absent", "", []string{"bug"}},
		{"named", "\n    queue:\n      types: [bug, incident]", []string{"bug", "incident"}},
		{"explicitly empty", "\n    queue:\n      types: []", []string{}},
		{"wildcard", "\n    queue:\n      types: [\"*\"]", []string{"*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := writeCfg(t, `workspace: w
provider: claude
sources:
  tracker:
    adapter: exec
    command: /bin/true`+tc.yaml+`
notes:
  dir: notes
`)
			cfg, err := Load(root)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := cfg.QueueTypes(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("QueueTypes = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestQueueIsTrackerOnly(t *testing.T) {
	root := writeCfg(t, `workspace: w
provider: claude
sources:
  helpdesk:
    adapter: zendesk
    subdomain: acme
    email: a@b.c
    apiToken: env:T
    queue:
      types: [bug]
notes:
  dir: notes
`)
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "sources.helpdesk.queue") {
		t.Fatalf("Load error = %v, want a complaint about sources.helpdesk.queue", err)
	}
}
