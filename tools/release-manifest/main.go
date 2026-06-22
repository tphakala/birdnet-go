// Command release-manifest generates the BirdNET-Go release manifest
// (manifest.json) by querying the GitHub Releases API for the latest release on
// each distribution channel (stable, nightly, beta). It is run in CI as the
// final step of the release and nightly build pipelines, and on a daily
// schedule as a self-heal; the produced file is published as an asset on the
// dedicated "manifest" release.
//
// Usage:
//
//	GITHUB_TOKEN=... go run ./tools/release-manifest -repo tphakala/birdnet-go -output manifest.json
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/update/manifest"
)

// repoRe validates the owner/name form of -repo before it is interpolated into
// an API URL.
var repoRe = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

func main() {
	repo := flag.String("repo", "tphakala/birdnet-go", "GitHub repository in owner/repo form")
	output := flag.String("output", "manifest.json", "path to write the manifest JSON to")
	apiURL := flag.String("api-url", "https://api.github.com", "GitHub REST API base URL")
	ghcrImage := flag.String("ghcr-image", "", "GHCR image repository (without tag); defaults to ghcr.io/<repo>")
	dockerHubImage := flag.String("dockerhub-image", "", "Docker Hub image repository (without tag); defaults to <repo>")
	maxNotesLen := flag.Int("max-notes-len", 50000, "maximum release-notes length in bytes; 0 for unbounded")
	flag.Parse()

	if err := run(*repo, *output, *apiURL, *ghcrImage, *dockerHubImage, *maxNotesLen); err != nil {
		fmt.Fprintln(os.Stderr, "release-manifest:", err)
		os.Exit(1)
	}
}

func run(repo, output, apiURL, ghcrImage, dockerHubImage string, maxNotesLen int) error {
	if !repoRe.MatchString(repo) {
		return fmt.Errorf("invalid -repo %q: want owner/name", repo)
	}
	// Default the image repositories to the GitHub repo so the manifest is
	// correct on forks and renames instead of advertising a hardcoded upstream.
	if ghcrImage == "" {
		ghcrImage = "ghcr.io/" + strings.ToLower(repo)
	}
	if dockerHubImage == "" {
		dockerHubImage = strings.ToLower(repo)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := newGitHubClient(apiURL, os.Getenv("GITHUB_TOKEN"))
	opts := &buildOptions{
		Repo:           repo,
		GHCRImage:      ghcrImage,
		DockerHubImage: dockerHubImage,
		GeneratedAt:    time.Now().UTC(),
		MaxNotesLen:    maxNotesLen,
	}

	m, warnings, err := buildManifest(ctx, client, opts)
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	if err != nil {
		// No matching releases (e.g. a fresh fork) is not a pipeline failure:
		// log and skip publishing rather than failing an otherwise-successful
		// release run.
		if errors.Is(err, errNoChannels) {
			fmt.Fprintln(os.Stderr, "release-manifest:", err, "- nothing to publish, skipping")
			return nil
		}
		return err
	}

	data, err := m.JSON()
	if err != nil {
		return err
	}
	if err := os.WriteFile(output, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", output, err)
	}

	fmt.Fprintf(os.Stderr, "wrote %s (%d channel(s), %d warning(s))\n", output, len(m.Channels), len(warnings))
	for _, name := range sortedChannelNames(m) {
		fmt.Fprintf(os.Stderr, "  %-8s %s (%d asset(s))\n", name, m.Channels[name].Version, len(m.Channels[name].Assets))
	}
	return nil
}

func sortedChannelNames(m *manifest.Manifest) []string {
	order := []string{manifest.ChannelStable, manifest.ChannelBeta, manifest.ChannelNightly}
	var names []string
	for _, name := range order {
		if _, ok := m.Channels[name]; ok {
			names = append(names, name)
		}
	}
	// Append any channel not in the preferred order (future-proofing).
	for name := range m.Channels {
		if name != manifest.ChannelStable && name != manifest.ChannelBeta && name != manifest.ChannelNightly {
			names = append(names, name)
		}
	}
	return names
}
