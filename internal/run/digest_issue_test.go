package run

import "testing"

// TestIssueLinePrefersTheTitleOverAGreeting is the digest column a live run
// filled with "Peace be upon you." for every row: the complaint is the
// customer's own words, and an Arabic support thread opens with a
// salutation that firstSentence lands on and that says nothing about the
// ticket.
func TestIssueLinePrefersTheTitleOverAGreeting(t *testing.T) {
	const arabic = "السلام عليكم. التصدير لا يعمل عندما يحتوي الطلب على أكثر من ٥٠٠ بند. الملف ينزل فارغًا."

	cases := []struct {
		name, title, complaint, want string
	}{
		{
			name:      "the title wins when there is one",
			title:     "Export returns an empty file over 500 lines",
			complaint: "Peace be upon you. The export downloads an empty file.",
			want:      "Export returns an empty file over 500 lines",
		},
		{
			name:      "an English greeting is skipped",
			complaint: "Peace be upon you. The export downloads an empty file.",
			want:      "The export downloads an empty file.",
		},
		{
			name:      "an Arabic greeting is skipped",
			complaint: arabic,
			want:      "التصدير لا يعمل عندما يحتوي الطلب على أكثر من ٥٠٠ بند.",
		},
		{
			name:      "a greeting on its own line is skipped",
			complaint: "مرحبا\nالفاتورة تظهر مرتين في كشف الحساب.",
			want:      "الفاتورة تظهر مرتين في كشف الحساب.",
		},
		{
			name:      "a first sentence that is not a greeting is kept",
			complaint: "The export fails for orders over 500 lines. It started on Tuesday.",
			want:      "The export fails for orders over 500 lines.",
		},
		{
			name:      "a greeting opener carrying the complaint is kept",
			complaint: "Hello, the export has failed every night since Tuesday. Please look.",
			want:      "Hello, the export has failed every night since Tuesday.",
		},
		{
			name:      "a complaint that is nothing but a greeting still says something",
			complaint: "السلام عليكم.",
			want:      "السلام عليكم.",
		},
		{
			name:      "a greeting that is not the first line is kept",
			complaint: "The report is empty. Thanks, and hello.",
			want:      "The report is empty.",
		},
		{
			name:      "an empty complaint stays empty",
			complaint: "   ",
			want:      "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := issueLine(c.title, c.complaint); got != c.want {
				t.Errorf("issueLine(%q, %q) = %q, want %q", c.title, c.complaint, got, c.want)
			}
		})
	}
}
