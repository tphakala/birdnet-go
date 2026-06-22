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
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/tphakala/birdnet-go/internal/update/manifest"
)

func main() {
	repo := flag.String("repo", "tphakala/birdnet-go", "GitHub repository in owner/repo form")
	output := flag.String("output", "manifest.json", "path to write the manifest JSON to")
	apiURL := flag.String("api-url", "https://api.github.com", "GitHub REST API base URL")
	ghcrImage := flag.String("ghcr-image", "ghcr.io/tphakala/birdnet-go", "GHCR image repository (without tag); empty to omit")
	dockerHubImage := flag.String("dockerhub-image", "tphakala/birdnet-go", "Docker Hub image repository (without tag); empty to omit")
	maxNotesLen := flag.Int("max-notes-len", 50000, "maximum release-notes length in bytes; 0 for unbounded")
	flag.Parse()

	if err := run(*repo, *output, *apiURL, *ghcrImage, *dockerHubImage, *maxNotesLen); err != nil {
		fmt.Fprintln(os.Stderr, "release-manifest:", err)
		os.Exit(1)
	}
}

func run(repo, output, apiURL, ghcrImage, dockerHubImage string, maxNotesLen int) error {
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
