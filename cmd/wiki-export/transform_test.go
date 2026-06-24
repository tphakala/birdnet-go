package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sourceBasenames mirrors the doc/wiki page set (lower-cased basenames without
// the .md extension), including the four migrated native pages.
var sourceBasenames = []string{
	"building",
	"cloudflare_tunnel_guide",
	"configuration-reference",
	"database-doctor",
	"detection-pipeline",
	"docker_compose_guide",
	"external-media",
	"faq",
	"file-analysis",
	"guide",
	"hardware",
	"index",
	"installation",
	"onnx-runtime-installation",
	"realtime-analysis",
	"rtsp-troubleshooting",
	"security",
	"telemetry",
	"telemetry-privacy",
	"telemetry-setup",
	"training-a-custom-classifier",
}

func TestWikiPageName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"guide":                        "BirdNET-Go-Guide",
		"faq":                          "FAQ",
		"index":                        "Home",
		"onnx-runtime-installation":    "ONNX-Runtime-Installation",
		"database-doctor":              "Database-Doctor",
		"file-analysis":                "File-Analysis",
		"realtime-analysis":            "Realtime-Analysis",
		"training-a-custom-classifier": "Training-a-Custom-Classifier",
		// Pass-through: page name equals the source basename.
		"installation":            "installation",
		"configuration-reference": "configuration-reference",
		"detection-pipeline":      "detection-pipeline",
		"docker_compose_guide":    "docker_compose_guide",
		"telemetry-privacy":       "telemetry-privacy",
	}
	for in, want := range cases {
		assert.Equalf(t, want, wikiPageName(in), "wikiPageName(%q)", in)
	}
}

func TestRewriteLinks(t *testing.T) {
	t.Parallel()
	idx := buildPageIndex(sourceBasenames)
	const dir = "doc/wiki"

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "relative md link to a pass-through page drops extension",
			in:   "See the [installation guide](installation.md).",
			want: "See the [installation guide](installation).",
		},
		{
			name: "md link with anchor to a remapped page uses wiki slug",
			in:   "Read about [deep detection](guide.md#deep-detection).",
			want: "Read about [deep detection](BirdNET-Go-Guide#deep-detection).",
		},
		{
			name: "sibling md link to a remapped page uses its wiki slug",
			in:   "See [ONNX setup](onnx-runtime-installation.md).",
			want: "See [ONNX setup](ONNX-Runtime-Installation).",
		},
		{
			name: "relative link to a non-wiki file sharing a wiki basename is not mapped to the wiki page",
			in:   "Edit [the template](../../internal/installation.md).",
			want: "Edit [the template](https://github.com/tphakala/birdnet-go/blob/main/internal/installation.md).",
		},
		{
			name: "faq remaps to title-case slug",
			in:   "Check the [FAQ](faq.md).",
			want: "Check the [FAQ](FAQ).",
		},
		{
			name: "bare unicode-hyphen slug is normalized",
			in:   "Locales are [documented here](BirdNET‐Go-Guide#supported-locales-for-species-labels).",
			want: "Locales are [documented here](BirdNET-Go-Guide#supported-locales-for-species-labels).",
		},
		{
			name: "absolute self-wiki URL with percent-encoded hyphen becomes relative slug",
			in:   "[guide](https://github.com/tphakala/birdnet-go/wiki/BirdNET%E2%80%90Go-Guide#deep-detection)",
			want: "[guide](BirdNET-Go-Guide#deep-detection)",
		},
		{
			name: "absolute self-wiki URL with .md suffix is cleaned",
			in:   "[install](https://github.com/tphakala/birdnet-go/wiki/installation.md)",
			want: "[install](installation)",
		},
		{
			name: "repo-root file link becomes absolute blob URL",
			in:   "See [privacy policy](../../PRIVACY.md).",
			want: "See [privacy policy](https://github.com/tphakala/birdnet-go/blob/main/PRIVACY.md).",
		},
		{
			name: "repo file with subpath becomes absolute blob URL",
			in:   "Use [compose](../../Docker/docker-compose.yml).",
			want: "Use [compose](https://github.com/tphakala/birdnet-go/blob/main/Docker/docker-compose.yml).",
		},
		{
			name: "external URL is left untouched",
			in:   "Stream to [OBS](https://obsproject.com/fi) overlays.",
			want: "Stream to [OBS](https://obsproject.com/fi) overlays.",
		},
		{
			name: "same-page anchor is left untouched",
			in:   "Jump to [the section](#installing-from-release-tarballs).",
			want: "Jump to [the section](#installing-from-release-tarballs).",
		},
		{
			name: "absolute image URL is left untouched",
			in:   "![folder](https://raw.githubusercontent.com/tphakala/birdnet-go/main/doc/wiki/images/x.PNG)",
			want: "![folder](https://raw.githubusercontent.com/tphakala/birdnet-go/main/doc/wiki/images/x.PNG)",
		},
		{
			name: "relative image link is left untouched",
			in:   "![diagram](images/diagram.png)",
			want: "![diagram](images/diagram.png)",
		},
		{
			name: "link title is preserved while target is rewritten",
			in:   `[hardware](hardware.md "Hardware notes")`,
			want: `[hardware](hardware "Hardware notes")`,
		},
		{
			name: "links inside fenced code blocks are not rewritten",
			in:   "```\nsee [x](installation.md)\n```\nand [y](installation.md)",
			want: "```\nsee [x](installation.md)\n```\nand [y](installation)",
		},
		{
			name: "links inside tilde-fenced code blocks are not rewritten",
			in:   "~~~\nsee [x](installation.md)\n~~~\nand [y](installation.md)",
			want: "~~~\nsee [x](installation.md)\n~~~\nand [y](installation)",
		},
		{
			name: "a tilde line inside a backtick fence does not close it",
			in:   "```\n~~~\n[x](installation.md)\n```\n[y](installation.md)",
			want: "```\n~~~\n[x](installation.md)\n```\n[y](installation)",
		},
		{
			name: "protocol-relative URL is treated as external",
			in:   "Served from [cdn](//cdn.example.com/lib.js).",
			want: "Served from [cdn](//cdn.example.com/lib.js).",
		},
		{
			name: "link escaping above the repo root is left untouched",
			in:   "Weird [x](../../../../etc/passwd).",
			want: "Weird [x](../../../../etc/passwd).",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, rewriteLinks(tc.in, dir, idx))
		})
	}
}

func TestInjectBanner(t *testing.T) {
	t.Parallel()

	t.Run("inserts banner after the first H1", func(t *testing.T) {
		t.Parallel()
		got := injectBanner("# Title\n\nBody text.\n", "installation.md")
		require.Contains(t, got, "# Title\n")
		assert.Contains(t, got, bannerMarker)
		assert.Contains(t, got, "doc/wiki/installation.md")
		// Banner must come after the H1 and before the body.
		h1 := strings.Index(got, "# Title")
		banner := strings.Index(got, bannerMarker)
		body := strings.Index(got, "Body text.")
		assert.Less(t, h1, banner, "banner should follow the H1")
		assert.Less(t, banner, body, "banner should precede the body")
	})

	t.Run("prepends banner when no H1 is present", func(t *testing.T) {
		t.Parallel()
		got := injectBanner("Just body text.\n", "faq.md")
		assert.Contains(t, got, bannerMarker)
		assert.Less(t, strings.Index(got, bannerMarker), strings.Index(got, "Just body text."))
	})

	t.Run("ignores an H1-looking line inside a leading code fence", func(t *testing.T) {
		t.Parallel()
		got := injectBanner("```bash\n# setup script\n```\n# Real Title\n\nBody.\n", "installation.md")
		// The banner must attach to the real H1, not the "# setup script"
		// comment inside the fenced block.
		fenceComment := strings.Index(got, "# setup script")
		banner := strings.Index(got, bannerMarker)
		realTitle := strings.Index(got, "# Real Title")
		assert.Less(t, fenceComment, banner, "banner must not land inside the code fence")
		assert.Less(t, realTitle, banner, "banner should follow the real H1")
	})

	t.Run("is idempotent", func(t *testing.T) {
		t.Parallel()
		once := injectBanner("# Title\n\nBody.\n", "installation.md")
		twice := injectBanner(once, "installation.md")
		assert.Equal(t, once, twice, "injecting the banner twice must not duplicate it")
	})
}
