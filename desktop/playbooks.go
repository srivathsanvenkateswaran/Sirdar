package main

import "github.com/srivathsanvenkateswaran/sirdar/internal/app"

// The playbooks a run's prompt is built from, as Settings › Playbooks reads
// and writes them. Every method forwards to app.Service, which is what
// makes them bridgeMethods rather than desktop-only openers: `sirdar serve`
// offers the same page over HTTP.

// Playbooks lists the workspace's playbooks in the order the prompt loads
// them, without their bodies.
func (b *Bridge) Playbooks(ws string) ([]app.PlaybookSummary, error) {
	rows, err := b.svc.Playbooks(ws)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []app.PlaybookSummary{}
	}
	return rows, nil
}

// Playbook returns one playbook's markdown.
func (b *Bridge) Playbook(ws, name string) (string, error) { return b.svc.Playbook(ws, name) }

// SavePlaybook writes a playbook's body and answers with its row.
func (b *Bridge) SavePlaybook(ws, name, body string) (app.PlaybookSummary, error) {
	return b.svc.SavePlaybook(ws, name, body)
}

// AddPlaybook creates a playbook. A name already taken is refused.
func (b *Bridge) AddPlaybook(ws, name, body string) (app.PlaybookSummary, error) {
	return b.svc.AddPlaybook(ws, name, body)
}

// DeletePlaybook moves a playbook into the playbooks directory's .trash.
func (b *Bridge) DeletePlaybook(ws, name string) error { return b.svc.DeletePlaybook(ws, name) }

// ScaffoldPlaybooks writes the starting set `sirdar init` scaffolds,
// skipping files already there, and answers with the list afterwards.
func (b *Bridge) ScaffoldPlaybooks(ws string) ([]app.PlaybookSummary, error) {
	rows, err := b.svc.ScaffoldPlaybooks(ws)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []app.PlaybookSummary{}
	}
	return rows, nil
}

// OpenPlaybook hands one playbook's file to the operator's own editor.
func (b *Bridge) OpenPlaybook(ws, name string) error { return b.svc.OpenPlaybook(ws, name) }
