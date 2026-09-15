package transcribe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeTranscriber writes a script that behaves the way whisper.cpp does:
// it takes -l, -otxt and -of, prints a detected-language line on stderr,
// and writes "<out>.txt". body is the transcript it produces.
func fakeTranscriber(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake transcriber is a shell script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-whisper")
	script := `#!/bin/sh
out=""
in=""
while [ $# -gt 0 ]; do
  case "$1" in
    -of) out="$2"; shift 2;;
    -l) shift 2;;
    -m) shift 2;;
    -otxt) shift;;
    *) in="$1"; shift;;
  esac
done
echo "auto-detected language: ar (p = 0.98)" >&2
if [ ! -f "$in" ]; then echo "no such input: $in" >&2; exit 3; fi
if [ -n "$out" ]; then
  printf '%s\n' "` + body + `" > "$out.txt"
else
  printf '%s\n' "` + body + `"
fi
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// audioFile writes a stand-in for a voice note. Nothing here decodes it:
// the command is the only thing that ever opens the bytes.
func audioFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("OggS fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTranscriber(t *testing.T, command string, tweak func(*Options)) *Transcriber {
	t.Helper()
	o := Options{Command: command, Formats: []string{"ogg", "mp3", "m4a"}}
	if tweak != nil {
		tweak(&o)
	}
	tx, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func TestTranscribesThroughTheConfiguredCommand(t *testing.T) {
	bin := fakeTranscriber(t, "الطلب لا يعمل")
	tx := newTranscriber(t, bin+" -l auto -otxt -of {out} {in}", nil)

	res, err := tx.Run(context.Background(), audioFile(t, "voice.ogg"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "الطلب لا يعمل" {
		t.Errorf("transcript %q", res.Text)
	}
	if res.Tool != filepath.Base(bin) {
		t.Errorf("tool %q", res.Tool)
	}
	if res.Language != "ar" {
		t.Errorf("language %q, want the language the tool detected", res.Language)
	}
	if got := Header("voice.ogg", res); !strings.Contains(got, "voice.ogg") ||
		!strings.Contains(got, res.Tool) || !strings.Contains(got, "ar") {
		t.Errorf("header %q names neither the file, the tool nor the language", got)
	}
}

// TestStdoutIsTheTranscriptWithoutAnOutputPlaceholder covers the second
// shape of command: one that just prints the text.
func TestStdoutIsTheTranscriptWithoutAnOutputPlaceholder(t *testing.T) {
	bin := fakeTranscriber(t, "spoken words")
	tx := newTranscriber(t, bin+" {in}", nil)

	res, err := tx.Run(context.Background(), audioFile(t, "voice.ogg"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "spoken words" {
		t.Errorf("transcript %q", res.Text)
	}
}

// TestOutdirPlaceholderIsFound covers the whisper CLI shape: the tool
// writes into a directory under a name this package did not choose.
func TestOutdirPlaceholderIsFound(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-py-whisper")
	script := "#!/bin/sh\n" +
		"echo \"Detected language: Arabic\"\n" +
		"printf 'from the outdir\\n' > \"$3/anything.txt\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tx := newTranscriber(t, bin+" {in} --output_dir {outdir}", nil)

	res, err := tx.Run(context.Background(), audioFile(t, "voice.ogg"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "from the outdir" {
		t.Errorf("transcript %q", res.Text)
	}
	if res.Language != "Arabic" {
		t.Errorf("language %q", res.Language)
	}
}

// TestPinnedLanguageBeatsDetection: a command told to transcribe Arabic is
// transcribing Arabic whatever it prints.
func TestPinnedLanguageBeatsDetection(t *testing.T) {
	bin := fakeTranscriber(t, "text")
	tx := newTranscriber(t, bin+" -l ar {in}", nil)
	res, err := tx.Run(context.Background(), audioFile(t, "voice.ogg"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Language != "ar" {
		t.Errorf("language %q, want the pinned one", res.Language)
	}
}

func TestATimeoutIsAnError(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "hang")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tx := newTranscriber(t, bin+" {in}", nil)
	tx.perFile = 200 * time.Millisecond

	start := time.Now()
	_, err := tx.Run(context.Background(), audioFile(t, "voice.ogg"))
	if err == nil {
		t.Fatal("a command that never returns should time out")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error %v, want it to say it timed out", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("the timeout did not stop the command: %s", time.Since(start))
	}
}

func TestFailingCommandIsAnErrorNotAPanic(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "broken")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'model not found' >&2\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tx := newTranscriber(t, bin+" {in}", nil)

	_, err := tx.Run(context.Background(), audioFile(t, "voice.ogg"))
	if err == nil {
		t.Fatal("a command that exits 2 should be an error")
	}
	if !strings.Contains(err.Error(), "model not found") {
		t.Errorf("the error does not carry what the tool said: %v", err)
	}
}

func TestMaxFilesStopsTheBatch(t *testing.T) {
	bin := fakeTranscriber(t, "text")
	tx := newTranscriber(t, bin+" {in}", func(o *Options) { o.MaxFiles = 2 })

	for i := 0; i < 2; i++ {
		if _, err := tx.Run(context.Background(), audioFile(t, "voice.ogg")); err != nil {
			t.Fatalf("file %d: %v", i, err)
		}
	}
	_, err := tx.Run(context.Background(), audioFile(t, "voice.ogg"))
	if !errors.Is(err, ErrOverCap) {
		t.Fatalf("the third file should be over the cap, got %v", err)
	}
}

func TestTheBatchBudgetStopsTheRun(t *testing.T) {
	bin := fakeTranscriber(t, "text")
	clock := time.Now()
	tx := newTranscriber(t, bin+" {in}", func(o *Options) {
		o.Now = func() time.Time { return clock }
	})
	if _, err := tx.Run(context.Background(), audioFile(t, "voice.ogg")); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(TotalTimeout + time.Second)
	_, err := tx.Run(context.Background(), audioFile(t, "voice.ogg"))
	if !errors.Is(err, ErrOverCap) {
		t.Fatalf("the batch budget should be spent, got %v", err)
	}
}

func TestHandlesOnlyTheConfiguredFormats(t *testing.T) {
	tx := newTranscriber(t, "/bin/echo {in}", nil)
	for _, c := range []struct {
		name, mime string
		want       bool
	}{
		{"voice.ogg", "audio/ogg", true},
		{"VOICE.OGG", "audio/ogg", true},
		{"note.mp3", "audio/mpeg", true},
		{"clip.wav", "audio/wav", false}, // not in this test's format list
		{"shot.png", "image/png", false},
		{"0123456789", "audio/ogg", true}, // no extension: the media type decides
		{"0123456789", "video/mp4", false},
	} {
		if got := tx.Handles(c.name, c.mime); got != c.want {
			t.Errorf("Handles(%q, %q) = %v, want %v", c.name, c.mime, got, c.want)
		}
	}
}

func TestSplitCommandKeepsQuotedPathsInOneArgument(t *testing.T) {
	argv, err := SplitCommand(`whisper-cli -m "/Users/a b/models/ggml large.bin" -l auto -of {out} {in}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"whisper-cli", "-m", "/Users/a b/models/ggml large.bin", "-l", "auto", "-of", "{out}", "{in}"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv %#v", argv)
	}

	argv, err = SplitCommand(`whisper-cli -m /Users/a\ b/model.bin '{in}'`)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"whisper-cli", "-m", "/Users/a b/model.bin", "{in}"}; !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv %#v", argv)
	}
}

func TestSplitCommandRefusesShellSyntax(t *testing.T) {
	for _, cmd := range []string{
		`whisper {in} | tee out.txt`,
		`whisper {in} > out.txt`,
		`whisper {in} && cat out.txt`,
		`whisper "{in}`,
		``,
	} {
		if _, err := SplitCommand(cmd); err == nil {
			t.Errorf("SplitCommand(%q) should have been refused", cmd)
		}
	}
}

func TestNewRefusesACommandThatNeverSeesTheAudio(t *testing.T) {
	if _, err := New(Options{Command: "whisper --help"}); err == nil {
		t.Fatal("a command with no {in} should be refused")
	}
}

// TestSpacesInTheAudioPathSurviveAsOneArgument is the substitution half of
// the same problem: the placeholder is replaced inside a token, so a run
// directory with a space in it is still one argv entry.
func TestSpacesInTheAudioPathSurviveAsOneArgument(t *testing.T) {
	bin := fakeTranscriber(t, "heard it")
	tx := newTranscriber(t, bin+" -otxt -of {out} {in}", nil)

	dir := filepath.Join(t.TempDir(), "run dir", "attachments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "voice note.ogg")
	if err := os.WriteFile(path, []byte("OggS"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := tx.Run(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "heard it" {
		t.Errorf("transcript %q", res.Text)
	}
}

// TestTheChildSeesNothingButPathHomeAndLang is the credential half: a
// triage run resolves helpdesk tokens into its own environment, and the
// operator's transcription command is not something they go to.
func TestTheChildSeesNothingButPathHomeAndLang(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "dump-env")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nenv\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tx := newTranscriber(t, bin+" {in}", func(o *Options) {
		o.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/tmp", "LANG=en_US.UTF-8", "ZOHO_TOKEN=secret", "AWS_SECRET_ACCESS_KEY=secret"}
	})

	res, err := tx.Run(context.Background(), audioFile(t, "voice.ogg"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "secret") {
		t.Fatalf("a credential reached the transcription command:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "HOME=/tmp") {
		t.Errorf("HOME did not reach the command:\n%s", res.Text)
	}
}
