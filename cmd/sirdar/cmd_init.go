package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
)

func init() { commands["init"] = cmdInit }

// gitExcludes are the paths a workspace should not commit by accident: the
// run directories, the register, and the eval reports. The register is
// excluded too, as the spec says, because committing it is the operator's
// choice. The exclusion also matters to `sirdar fix`, which refuses to
// start on a dirty working tree: without it, every earlier run's directory
// would read as uncommitted work.
var gitExcludes = []string{".sirdar/runs/", ".sirdar/register.jsonl", ".sirdar/eval/"}

// cmdInit scaffolds a workspace in the working directory. Unlike every
// other command it does not look for an existing workspace: it makes one.
func cmdInit(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("init", stderr, "usage: sirdar init [--templates] [--force]")
	templates := fs.Bool("templates", false, "write the default note templates into .sirdar/templates")
	force := fs.Bool("force", false, "overwrite an existing .sirdar/config.yaml")
	if _, ok := parseFlags(fs, args, 0, 0, stderr); !ok {
		return exitUsage
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}

	sirdarDir := filepath.Join(cwd, ".sirdar")
	if err := os.MkdirAll(sirdarDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}

	configPath := filepath.Join(sirdarDir, "config.yaml")
	if _, err := os.Stat(configPath); err == nil && !*force {
		fmt.Fprintf(stderr, "sirdar: %s already exists; pass --force to overwrite it\n", configPath)
		return 1
	}
	body := strings.Replace(config.DefaultConfigYAML, "<name>", filepath.Base(cwd), 1)
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s\n", rel(cwd, configPath))

	playbooks := filepath.Join(sirdarDir, "playbooks")
	if err := prompt.ScaffoldPlaybooks(playbooks); err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "scaffolded %s\n", rel(cwd, playbooks))

	if *templates {
		dir := filepath.Join(sirdarDir, "templates")
		if err := writeTemplates(dir); err != nil {
			fmt.Fprintf(stderr, "sirdar: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "wrote the default note templates to %s\n", rel(cwd, dir))
		fmt.Fprintln(stdout, "set notes.templates to that directory to use them")
	}

	added, err := addGitExcludes(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	for _, line := range added {
		fmt.Fprintf(stdout, "excluded %s in .git/info/exclude\n", line)
	}

	fmt.Fprintln(stdout, "\nnext: edit .sirdar/config.yaml for this workspace, then run 'sirdar doctor'")
	return 0
}

func writeTemplates(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, kind := range []note.Kind{note.Triage, note.RCA, note.Resolution} {
		body, err := note.DefaultTemplate(kind)
		if err != nil {
			return err
		}
		path := filepath.Join(dir, string(kind)+".md.tmpl")
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// addGitExcludes appends the run directory and register to
// .git/info/exclude, skipping lines that are already there so a repeated
// init does not pile them up. A directory that is not a git checkout is
// not an error: there is simply nothing to exclude.
func addGitExcludes(root string) ([]string, error) {
	gitDir := filepath.Join(root, ".git")
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		return nil, nil
	}
	infoDir := filepath.Join(gitDir, "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(infoDir, "exclude")

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(existing), "\n") {
		have[strings.TrimSpace(line)] = true
	}

	var add []string
	for _, line := range gitExcludes {
		if !have[line] {
			add = append(add, line)
		}
	}
	if len(add) == 0 {
		return nil, nil
	}

	var buf bytes.Buffer
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		buf.WriteByte('\n')
	}
	for _, line := range add {
		buf.WriteString(line + "\n")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(buf.Bytes()); err != nil {
		return nil, err
	}
	return add, nil
}

// rel shortens a path for display when it sits under base.
func rel(base, path string) string {
	if r, err := filepath.Rel(base, path); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return path
}

// newFlagSet returns a FlagSet that reports errors and usage on stderr in
// sirdar's own format rather than Go's default.
func newFlagSet(name string, stderr io.Writer, usageLine string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, usageLine)
		fs.PrintDefaults()
	}
	return fs
}

// parseFlags parses args, allowing flags and positional arguments in any
// order (`sirdar rca OMNI-1 --pr URL` is the documented shape), and checks
// the positional count: at least min, and at most max unless max is
// negative. It prints usage and returns false when the invocation is wrong,
// so callers can simply return exitUsage.
func parseFlags(fs *flag.FlagSet, args []string, min, max int, stderr io.Writer) ([]string, bool) {
	// A literal "--" ends flag parsing: everything after it is positional
	// however much it looks like a flag.
	var literal []string
	for i, a := range args {
		if a == "--" {
			args, literal = args[:i], args[i+1:]
			break
		}
	}

	// flag.Parse stops at the first non-flag argument, so peel positional
	// arguments off one at a time and parse what follows each of them.
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			// flag has already reported the error and called fs.Usage.
			return nil, false
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
	positional = append(positional, literal...)

	switch n := len(positional); {
	case n < min:
		fmt.Fprintf(stderr, "sirdar %s: missing argument\n", fs.Name())
		fs.Usage()
		return nil, false
	case max >= 0 && n > max:
		fmt.Fprintf(stderr, "sirdar %s: too many arguments\n", fs.Name())
		fs.Usage()
		return nil, false
	}
	return positional, true
}
