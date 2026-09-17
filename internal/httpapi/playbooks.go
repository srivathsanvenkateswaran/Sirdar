package httpapi

import (
	"net/http"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
)

/*
The playbooks routes: the markdown files under `.sirdar/playbooks/` that
every run's prompt is built from, as Settings › Playbooks reads and writes
them.

	GET    /api/workspaces/{id}/playbooks              the list, no bodies
	POST   /api/workspaces/{id}/playbooks              create one
	POST   /api/workspaces/{id}/playbooks/scaffold     write the starting set
	GET    /api/workspaces/{id}/playbooks/{name}       one body, as markdown
	PUT    /api/workspaces/{id}/playbooks/{name}       write one body
	DELETE /api/workspaces/{id}/playbooks/{name}       move it to the trash
	POST   /api/workspaces/{id}/playbooks/{name}/open  open it in an editor

These are the only routes that write inside a workspace other than a run
directory, and the service confines them: the name is one plain filename,
the directory has to be under `.sirdar/`, and a delete moves the file
rather than unlinking it.
*/

// validPlaybookName writes the 400 itself when name could not be one a
// playbook is created under, the way validProvider does for a provider.
// app.Service checks the same name again before it writes anything; this is
// only the early answer with the shape in it.
func validPlaybookName(w http.ResponseWriter, name string) bool {
	if app.ValidPlaybookName(name) {
		return true
	}
	writeError(w, http.StatusBadRequest, "bad_request",
		"name must be two digits, a hyphen, a lowercase slug and .md — 20-logs.md;"+
			" the digits are what orders the playbooks in the prompt")
	return false
}

func (s *server) playbooks(w http.ResponseWriter, r *http.Request) {
	rows, err := s.svc.Playbooks(r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(rows))
}

func (s *server) playbook(w http.ResponseWriter, r *http.Request) {
	md, err := s.svc.Playbook(r.PathValue("id"), r.PathValue("name"))
	if err != nil {
		s.failDocument(w, err)
		return
	}
	writeMarkdown(w, md)
}

// addPlaybook is POST /api/workspaces/{id}/playbooks. A name already taken
// is a 409 rather than a silent overwrite: the editor is how an existing
// playbook is changed.
func (s *server) addPlaybook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	if !validPlaybookName(w, body.Name) {
		return
	}
	row, err := s.svc.AddPlaybook(r.PathValue("id"), body.Name, body.Body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// savePlaybook is PUT /api/workspaces/{id}/playbooks/{name}. The body is
// carried as a JSON field rather than as the request body itself so the
// cross-site guard's application/json rule covers it.
func (s *server) savePlaybook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Body string `json:"body"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	row, err := s.svc.SavePlaybook(r.PathValue("id"), r.PathValue("name"), body.Body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *server) deletePlaybook(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeletePlaybook(r.PathValue("id"), r.PathValue("name")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// scaffoldPlaybooks is POST /api/workspaces/{id}/playbooks/scaffold: the
// same starting set `sirdar init` writes, skipping every file already
// there. It answers with the list as it stands afterwards.
func (s *server) scaffoldPlaybooks(w http.ResponseWriter, r *http.Request) {
	rows, err := s.svc.ScaffoldPlaybooks(r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(rows))
}

// openPlaybook is POST /api/workspaces/{id}/playbooks/{name}/open: hand the
// file to whatever the machine running this server associates with
// markdown.
//
// It is refused on a listener other machines can reach, for the reason the
// fix route is: this starts a program on the operator's machine, and a
// server with no authentication of any kind must not offer that to whoever
// turns out to be able to reach the port. On the desktop shell the bridge
// calls the service directly and there is no listener in the way.
func (s *server) openPlaybook(w http.ResponseWriter, r *http.Request) {
	if !s.loopbackOnly {
		writeError(w, http.StatusForbidden, "forbidden",
			"opening a playbook starts an editor on the machine running this server, so it is refused on a"+
				" listener other machines can reach; edit it in this window instead")
		return
	}
	if err := s.svc.OpenPlaybook(r.PathValue("id"), r.PathValue("name")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
