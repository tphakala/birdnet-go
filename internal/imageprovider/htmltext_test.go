package imageprovider

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHTMLToText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "Some Photographer", "Some Photographer"},
		{"single span", "<span>Plain Author</span>", "Plain Author"},
		{"nested inline", "<bdi>Artist Name</bdi>", "Artist Name"},
		{"anchor inner text", `<a href="x">John Doe</a>`, "John Doe"},
		{"collapses whitespace", "  Foo\n\t  Bar  ", "Foo Bar"},
		{"decodes entities", "A &amp; B", "A & B"},
		{"skips script", "<p>Visible</p><script>var x=1;</script>", "Visible"},
		{"skips style", "<style>.c{color:red}</style><p>Text</p>", "Text"},
		{"empty input", "", ""},
		{"separates block text", "<p>rate</p><p>limit</p>", "rate limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, htmlToText(tt.in), "htmlToText(%q)", tt.in)
		})
	}
}
