package eval

import (
	"reflect"
	"testing"
)

func TestParseDiff(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    Diff
		wantErr string
	}{
		{
			name: "one edited file",
			in: `diff --git a/internal/export/csv.go b/internal/export/csv.go
index 1a2b3c4..5d6e7f8 100644
--- a/internal/export/csv.go
+++ b/internal/export/csv.go
@@ -40,7 +40,9 @@ func Export(w io.Writer) error {
 	rows, err := load()
 	if err != nil {
 		return err
 	}
-	buf := bytes.NewBuffer(nil)
+	enc := csv.NewWriter(w)
+	defer enc.Flush()
 	for _, row := range rows {
`,
			want: Diff{
				Added: 2, Removed: 1,
				Files: []FileDiff{{
					Path: "internal/export/csv.go", OldPath: "internal/export/csv.go",
					Added: 2, Removed: 1,
					Hunks: []Hunk{{OldStart: 40, OldLines: 7, NewStart: 40, NewLines: 9}},
				}},
			},
		},
		{
			name: "a new file has no old side",
			in: `diff --git a/internal/export/stream.go b/internal/export/stream.go
new file mode 100644
index 0000000..abcdef1
--- /dev/null
+++ b/internal/export/stream.go
@@ -0,0 +1,3 @@
+package export
+
+func Stream() {}
`,
			want: Diff{
				Added: 3,
				Files: []FileDiff{{
					Path: "internal/export/stream.go", New: true, Added: 3,
					Hunks: []Hunk{{OldStart: 0, OldLines: 0, NewStart: 1, NewLines: 3}},
				}},
			},
		},
		{
			name: "a deleted file keeps the name it had",
			in: `diff --git a/old/gone.go b/old/gone.go
deleted file mode 100644
--- a/old/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package old
-
`,
			want: Diff{
				Removed: 2,
				Files: []FileDiff{{
					Path: "old/gone.go", OldPath: "old/gone.go", Deleted: true, Removed: 2,
					Hunks: []Hunk{{OldStart: 1, OldLines: 2, NewStart: 0, NewLines: 0}},
				}},
			},
		},
		{
			name: "a pure rename carries both paths and no hunk",
			in: `diff --git a/a/one.go b/b/two.go
similarity index 100%
rename from a/one.go
rename to b/two.go
`,
			want: Diff{
				Files: []FileDiff{{Path: "b/two.go", OldPath: "a/one.go", Renamed: true}},
			},
		},
		{
			name: "a rename that also edits scores on the old path",
			in: `diff --git a/a/one.go b/b/two.go
similarity index 88%
rename from a/one.go
rename to b/two.go
--- a/a/one.go
+++ b/b/two.go
@@ -12,3 +12,3 @@
 keep
-old
+new
`,
			want: Diff{
				Added: 1, Removed: 1,
				Files: []FileDiff{{
					Path: "b/two.go", OldPath: "a/one.go", Renamed: true, Added: 1, Removed: 1,
					Hunks: []Hunk{{OldStart: 12, OldLines: 3, NewStart: 12, NewLines: 3}},
				}},
			},
		},
		{
			name: "several files in one diff",
			in: `diff --git a/x.go b/x.go
--- a/x.go
+++ b/x.go
@@ -1 +1,2 @@
 a
+b
diff --git a/y.go b/y.go
--- a/y.go
+++ b/y.go
@@ -5,2 +6,1 @@
-c
-d
+e
`,
			want: Diff{
				Added: 2, Removed: 2,
				Files: []FileDiff{
					{Path: "x.go", OldPath: "x.go", Added: 1, Hunks: []Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2}}},
					{Path: "y.go", OldPath: "y.go", Added: 1, Removed: 2, Hunks: []Hunk{{OldStart: 5, OldLines: 2, NewStart: 6, NewLines: 1}}},
				},
			},
		},
		{
			// The hunk counts, not the line shapes, say where a hunk
			// ends: a removed line that reads like a header is content.
			name: "a diff of a diff is content, not headers",
			in: `diff --git a/fixture.diff b/fixture.diff
--- a/fixture.diff
+++ b/fixture.diff
@@ -1,3 +1,3 @@
 diff --git a/inner.go b/inner.go
---- a/inner.go
-+++ b/inner.go
+--- a/other.go
++++ b/other.go
`,
			want: Diff{
				Added: 2, Removed: 2,
				Files: []FileDiff{{
					Path: "fixture.diff", OldPath: "fixture.diff", Added: 2, Removed: 2,
					Hunks: []Hunk{{OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 3}},
				}},
			},
		},
		{
			name: "no newline at end of file belongs to neither side",
			in: `diff --git a/x.txt b/x.txt
--- a/x.txt
+++ b/x.txt
@@ -1 +1 @@
-old
\ No newline at end of file
+new
\ No newline at end of file
`,
			want: Diff{
				Added: 1, Removed: 1,
				Files: []FileDiff{{
					Path: "x.txt", OldPath: "x.txt", Added: 1, Removed: 1,
					Hunks: []Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1}},
				}},
			},
		},
		{
			name: "a hunk header with no count means one line",
			in: `--- a/z.go
+++ b/z.go
@@ -7 +7 @@
-a
+b
`,
			want: Diff{
				Added: 1, Removed: 1,
				Files: []FileDiff{{
					Path: "z.go", OldPath: "z.go", Added: 1, Removed: 1,
					Hunks: []Hunk{{OldStart: 7, OldLines: 1, NewStart: 7, NewLines: 1}},
				}},
			},
		},
		{
			name: "a quoted path with a space in it",
			in: `diff --git "a/src/two words.go" "b/src/two words.go"
--- "a/src/two words.go"
+++ "b/src/two words.go"
@@ -1 +1 @@
-a
+b
`,
			want: Diff{
				Added: 1, Removed: 1,
				Files: []FileDiff{{
					Path: "src/two words.go", OldPath: "src/two words.go", Added: 1, Removed: 1,
					Hunks: []Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1}},
				}},
			},
		},
		{
			name: "an empty diff",
			in:   "",
			want: Diff{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseDiff(tc.in)
			if got.Added != tc.want.Added || got.Removed != tc.want.Removed {
				t.Errorf("lines: got +%d -%d, want +%d -%d", got.Added, got.Removed, tc.want.Added, tc.want.Removed)
			}
			if len(got.Files) != len(tc.want.Files) {
				t.Fatalf("files: got %d (%v), want %d", len(got.Files), got.Paths(), len(tc.want.Files))
			}
			for i := range tc.want.Files {
				if !reflect.DeepEqual(got.Files[i], tc.want.Files[i]) {
					t.Errorf("file %d:\n got %+v\nwant %+v", i, got.Files[i], tc.want.Files[i])
				}
			}
		})
	}
}

func TestFileDiffBaseIsTheNameAtTheSharedCommit(t *testing.T) {
	cases := []struct {
		name string
		file FileDiff
		want string
	}{
		{"an edit", FileDiff{Path: "x.go", OldPath: "x.go"}, "x.go"},
		{"a rename", FileDiff{Path: "b/two.go", OldPath: "a/one.go"}, "a/one.go"},
		{"a new file has only the name it was given", FileDiff{Path: "new.go", New: true}, "new.go"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.file.Base(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		`internal\export\csv.go`: "internal/export/csv.go",
		"./internal/csv.go":      "internal/csv.go",
		"/internal/csv.go":       "internal/csv.go",
		"  csv.go  ":             "csv.go",
		"":                       "",
	}
	for in, want := range cases {
		if got := NormalizePath(in); got != want {
			t.Errorf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}
