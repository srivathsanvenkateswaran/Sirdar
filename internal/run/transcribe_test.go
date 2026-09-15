package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// fakeTranscriberScript writes a stand-in for whisper.cpp: it takes
// "-of <stem>" and writes "<stem>.txt", announces a detected language on
// stderr, and produces the same fixed transcript every time. Nothing in
// the tests below installs or needs a real model.
func fakeTranscriberScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake transcriber is a shell script")
	}
	path := filepath.Join(t.TempDir(), "fake-whisper")
	script := `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -of) out="$2"; shift 2;;
    -l) shift 2;;
    -otxt) shift;;
    *) shift;;
  esac
done
echo "auto-detected language: ar (p = 0.98)" >&2
printf '%s\n' "` + body + `" > "$out.txt"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// transcribeWorkspace is the default test workspace plus a transcription
// block running command.
func transcribeWorkspace(t *testing.T, command string, extra string) *config.Config {
	t.Helper()
	return newWorkspaceWith(t, configYAML+"attachments:\n  transcribe:\n    command: "+command+"\n"+extra)
}

// voiceNotes is the ticket that prompted all of this: a thread carrying
// WhatsApp voice notes the session cannot open.
func voiceNotes(sizes ...int) stubHelpdesk {
	files := make([]stubAttachment, 0, len(sizes))
	for i, size := range sizes {
		name := "PTT-2026" + string(rune('a'+i)) + ".ogg"
		files = append(files, stubAttachment{
			Attachment: ticket.Attachment{
				ID: "a" + string(rune('1'+i)), Name: name, MIME: "audio/ogg",
				Path: "attachments/" + name,
			},
			bytes: size,
		})
	}
	return stubHelpdesk{files: files}
}

// withRealEnv gives the runner the process environment, which is what a
// real run hands the transcription command: the child still only sees
// PATH, HOME and LANG.
func withRealEnv(r *Runner) *Runner {
	r.Env = os.Environ()
	return r
}

func bundleAttachments(t *testing.T, dir string) []ticket.Attachment {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "bundle", "ticket.json"))
	if err != nil {
		t.Fatal(err)
	}
	var b ticket.Bundle
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	return b.Attachments
}

// TestVoiceNotesAreTranscribedIntoTheBundle is the whole feature end to
// end: the audio a session cannot open becomes a text file beside it, the
// manifest says so, and the prompt points the agent at it.
func TestVoiceNotesAreTranscribedIntoTheBundle(t *testing.T) {
	bin := fakeTranscriberScript(t, "الطلب لا يعمل")
	cfg := transcribeWorkspace(t, bin+" -l auto -otxt -of {out} {in}", "")
	r := withRealEnv(newRunner(cfg, &stubProvider{script: replay(finalEvent(triageDoc))}, stubTracker{}, voiceNotes(64, 64)))

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	dir := runDir(t, cfg, out)

	transcript := filepath.Join(dir, "bundle", "attachments", "PTT-2026a.ogg.transcript.txt")
	body := readFile(t, transcript)
	if !strings.Contains(body, "الطلب لا يعمل") {
		t.Errorf("the transcript does not carry what the tool produced:\n%s", body)
	}
	header := strings.SplitN(body, "\n", 2)[0]
	for _, want := range []string{"PTT-2026a.ogg", filepath.Base(bin), "ar"} {
		if !strings.Contains(header, want) {
			t.Errorf("the header %q does not name %q", header, want)
		}
	}

	// The audio itself stays: it is the source of anything the note
	// quotes, and an engineer checking that quotation has to hear it.
	if _, err := os.Stat(filepath.Join(dir, "bundle", "attachments", "PTT-2026a.ogg")); err != nil {
		t.Errorf("the transcribed audio was dropped from the bundle: %v", err)
	}

	atts := bundleAttachments(t, dir)
	if len(atts) != 2 {
		t.Fatalf("manifest holds %d attachments, want both voice notes", len(atts))
	}
	for _, a := range atts {
		if !a.Transcribed() {
			t.Errorf("the manifest does not mark %q transcribed: %+v", a.Name, a)
		}
		if a.TranscriptLanguage != "ar" {
			t.Errorf("%q: language %q", a.Name, a.TranscriptLanguage)
		}
	}

	promptText := readFile(t, filepath.Join(dir, "prompt.md"))
	if !strings.Contains(promptText, "attachments/PTT-2026a.ogg.transcript.txt") {
		t.Errorf("the prompt does not list the transcript:\n%s", promptText)
	}
	if !strings.Contains(promptText, "2 audio attachments were transcribed") {
		t.Errorf("the prompt does not tell the agent transcripts exist:\n%s", promptText)
	}
	for _, w := range out.State.Warnings {
		if strings.Contains(w, "PTT-2026a.ogg") {
			t.Errorf("a transcribed voice note was still reported as unread: %q", w)
		}
	}

	thread := readFile(t, filepath.Join(dir, "bundle", "thread.md"))
	if !strings.Contains(thread, "transcript: attachments/PTT-2026a.ogg.transcript.txt") {
		t.Errorf("the conversation does not point at the transcript:\n%s", thread)
	}
}

// TestTranscriptionIsOffWithoutTheConfigBlock is the default a workspace
// gets: a voice note is dropped and named, exactly as before.
func TestTranscriptionIsOffWithoutTheConfigBlock(t *testing.T) {
	cfg := newWorkspace(t)
	if _, ok := cfg.TranscribeOptions(); ok {
		t.Fatal("a workspace with no transcribe block should transcribe nothing")
	}
	r := withRealEnv(newRunner(cfg, &stubProvider{script: replay(finalEvent(triageDoc))}, stubTracker{}, voiceNotes(64)))

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	dir := runDir(t, cfg, out)

	if _, err := os.Stat(filepath.Join(dir, "bundle", "attachments", "PTT-2026a.ogg")); !os.IsNotExist(err) {
		t.Errorf("the voice note should have been dropped: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "bundle", "attachments", "*.transcript.txt")); len(matches) != 0 {
		t.Errorf("a transcript was written with no command configured: %v", matches)
	}
	if !hasWarningContaining(out.State.Warnings, "PTT-2026a.ogg") {
		t.Errorf("the dropped voice note is not named in a warning: %v", out.State.Warnings)
	}
}

// TestAudioOverMaxSecondsIsSkipped covers the length check, which needs
// ffprobe to answer. A fake one stands in for it, the way a fake
// transcriber stands in for whisper.
func TestAudioOverMaxSecondsIsSkipped(t *testing.T) {
	bin := fakeTranscriberScript(t, "never reached")
	binDir := t.TempDir()
	probe := "#!/bin/sh\necho 912.4\n"
	if err := os.WriteFile(filepath.Join(binDir, "ffprobe"), []byte(probe), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := transcribeWorkspace(t, bin+" -otxt -of {out} {in}", "    maxSeconds: 300\n")
	r := withRealEnv(newRunner(cfg, &stubProvider{script: replay(finalEvent(triageDoc))}, stubTracker{}, voiceNotes(64)))

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	dir := runDir(t, cfg, out)

	if matches, _ := filepath.Glob(filepath.Join(dir, "bundle", "attachments", "*.transcript.txt")); len(matches) != 0 {
		t.Errorf("a 15-minute recording should not have been transcribed: %v", matches)
	}
	if !hasWarningContaining(out.State.Warnings, "912s long") {
		t.Errorf("the warning does not say how long the audio was: %v", out.State.Warnings)
	}
	if !hasWarningContaining(out.State.Warnings, "maxSeconds") {
		t.Errorf("the warning does not name the limit it broke: %v", out.State.Warnings)
	}
	if _, err := os.Stat(filepath.Join(dir, "bundle", "attachments", "PTT-2026a.ogg")); !os.IsNotExist(err) {
		t.Errorf("an untranscribed voice note should have been dropped: %v", err)
	}
}

// TestMaxFilesLeavesTheRestUnreadAndSaysSoOnce: the cap is reported once,
// and every file past it is reported the way any unread attachment is.
func TestMaxFilesLeavesTheRestUnreadAndSaysSoOnce(t *testing.T) {
	bin := fakeTranscriberScript(t, "heard it")
	cfg := transcribeWorkspace(t, bin+" -otxt -of {out} {in}", "    maxFiles: 1\n")
	r := withRealEnv(newRunner(cfg, &stubProvider{script: replay(finalEvent(triageDoc))}, stubTracker{}, voiceNotes(64, 64, 64)))

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	dir := runDir(t, cfg, out)

	matches, _ := filepath.Glob(filepath.Join(dir, "bundle", "attachments", "*.transcript.txt"))
	if len(matches) != 1 {
		t.Fatalf("maxFiles: 1 produced %d transcripts: %v", len(matches), matches)
	}
	caps := 0
	for _, w := range out.State.Warnings {
		if strings.Contains(w, "transcription stopped") {
			caps++
		}
	}
	if caps != 1 {
		t.Errorf("the cap was reported %d times, want once: %v", caps, out.State.Warnings)
	}
	for _, name := range []string{"PTT-2026b.ogg", "PTT-2026c.ogg"} {
		if !hasWarningContaining(out.State.Warnings, name) {
			t.Errorf("%s is not named as unread: %v", name, out.State.Warnings)
		}
	}
}

// TestAFailingTranscriberIsAWarningNotARunFailure: nothing about audio is
// allowed to stop a triage.
func TestAFailingTranscriberIsAWarningNotARunFailure(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "broken")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'failed to load model' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := transcribeWorkspace(t, bin+" {in}", "")
	r := withRealEnv(newRunner(cfg, &stubProvider{script: replay(finalEvent(triageDoc))}, stubTracker{}, voiceNotes(64)))

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("a failed transcription failed the run: %q %q", out.State.Status, out.State.Reason)
	}
	if !hasWarningContaining(out.State.Warnings, "failed to load model") {
		t.Errorf("the warning does not carry what the tool said: %v", out.State.Warnings)
	}
}

// TestTheNoteNamesTheAttachmentsNobodyRead checks the one thing a human
// reads: the note says which attachments never reached the session, and a
// transcribed one is not among them.
func TestTheNoteNamesTheAttachmentsNobodyRead(t *testing.T) {
	bin := fakeTranscriberScript(t, "heard it")
	cfg := transcribeWorkspace(t, bin+" -otxt -of {out} {in}", "")
	hd := voiceNotes(64)
	hd.files = append(hd.files, stubAttachment{
		Attachment: ticket.Attachment{ID: "v1", Name: "screen.mov", MIME: "video/quicktime", Path: "attachments/screen.mov"},
		bytes:      64,
	})
	r := withRealEnv(newRunner(cfg, &stubProvider{script: replay(finalEvent(triageDoc))}, stubTracker{}, hd))

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if len(out.State.Notes) == 0 {
		t.Fatalf("no note was written: %+v", out.State)
	}
	note := readFile(t, out.State.Notes[0])
	if !strings.Contains(note, "## Attachments not reviewed") {
		t.Fatalf("no \"Attachments not reviewed\" section:\n%s", note)
	}
	for _, want := range []string{"screen.mov", "video/quicktime", "cannot be opened in this session"} {
		if !strings.Contains(note, want) {
			t.Errorf("the attachments-not-reviewed section is missing %q:\n%s", want, note)
		}
	}
	if strings.Contains(note, "PTT-2026a.ogg") {
		t.Errorf("a transcribed voice note is listed as unreviewed:\n%s", note)
	}
}

// TestTranscriptIsCappedAtAttachmentsMaxBytes is the round-1 fix: the
// transcript file writeTranscript puts in the bundle counts toward the
// same attachments.maxBytes cap as every other kept attachment, rather
// than landing at whatever size transcribe.Result.Text happened to be.
func TestTranscriptIsCappedAtAttachmentsMaxBytes(t *testing.T) {
	long := strings.Repeat("x", 2000)
	bin := fakeTranscriberScript(t, long)
	cfg := newWorkspaceWith(t, configYAML+
		"attachments:\n  maxBytes: 300\n  transcribe:\n    command: "+bin+" -otxt -of {out} {in}\n")
	r := withRealEnv(newRunner(cfg, &stubProvider{script: replay(finalEvent(triageDoc))}, stubTracker{}, voiceNotes(64)))

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	dir := runDir(t, cfg, out)

	transcript := filepath.Join(dir, "bundle", "attachments", "PTT-2026a.ogg.transcript.txt")
	body := readFile(t, transcript)
	if len(body) > 320 {
		t.Fatalf("the transcript on disk is %d bytes, want it capped near the 300-byte attachments.maxBytes: %q", len(body), body)
	}
	if strings.Contains(body, long) {
		t.Fatalf("the full transcript reached disk despite attachments.maxBytes: %q", body)
	}
}
