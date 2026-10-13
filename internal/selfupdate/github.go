package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"

	"github.com/clementherve/godio/internal/lang"
)

// Repo is the repository releases are taken from.
const Repo = "clementherve/godio"

const apiBase = "https://api.github.com/repos/" + Repo + "/releases"

// Asset is one downloadable file on a release.
type Asset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"browser_download_url"`
	// Digest is GitHub's own "sha256:<hex>" of the uploaded file. It is what
	// the downloader checks against, which removes any need for the
	// SHA256SUMS.txt the release also carries.
	Digest string `json:"digest"`
}

// Release is the subset of a GitHub release this package needs.
type Release struct {
	Tag        string  `json:"tag_name"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Assets     []Asset `json:"assets"`
}

// ErrNotPublic reports a 404 from the releases API.
//
// For this repository that almost always means one thing — the repository is
// private and the updater does not authenticate — and saying so beats
// reporting "404 Not Found" to someone who can see the release in a browser
// while logged in.
// It is a type rather than errors.New so the message is built when it is
// shown, not when the package is initialised — which happens before the
// interface language has been chosen.
var ErrNotPublic error = notPublicError{}

type notPublicError struct{}

func (notPublicError) Error() string {
	return lang.T().Err.NotPublic1 + lang.T().Err.NotPublic2 + Repo + lang.T().Err.NotPublic3
}

// binaryNames lists the published executables, in the order they are swapped.
//
// The releases carry windows/amd64 binaries only, so there is nothing to
// install anywhere else; Check still works, which is what makes
// « update --check » usable during development.
func binaryNames() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	return []string{"godio.exe", "godio-tray.exe"}
}

// Latest returns the most recent release.
//
// /releases/latest already excludes drafts and pre-releases, so including
// pre-releases means listing instead and taking the first entry — the list
// endpoint is ordered by creation date, newest first.
func Latest(ctx context.Context, prerelease bool) (Release, error) {
	if !prerelease {
		var rel Release
		if err := getJSON(ctx, apiBase+"/latest", &rel); err != nil {
			return Release{}, err
		}
		return rel, nil
	}

	var releases []Release
	if err := getJSON(ctx, apiBase+"?per_page=20", &releases); err != nil {
		return Release{}, err
	}
	for _, rel := range releases {
		if !rel.Draft {
			return rel, nil
		}
	}
	return Release{}, errors.New(lang.T().Err.NoRelease)
}

// Binaries returns the release's godio executables.
func Binaries(rel Release) ([]Asset, error) {
	return binariesFrom(rel, binaryNames())
}

// binariesFrom is Binaries with the expected names passed in, so the matching
// can be tested on a platform that publishes no binaries of its own.
func binariesFrom(rel Release, names []string) ([]Asset, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf(lang.T().Err.WindowsOnlyFmt,
			runtime.GOOS, runtime.GOARCH)
	}

	out := make([]Asset, 0, len(names))
	for _, want := range names {
		found := false
		for _, a := range rel.Assets {
			if a.Name == want {
				out = append(out, a)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf(lang.T().Err.ReleaseMissingFmt, rel.Tag, want)
		}
	}
	return out, nil
}

// getJSON fetches and decodes one API response.
func getJSON(ctx context.Context, url string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "godio")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf(lang.T().Err.GitHubQueryFmtW, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return ErrNotPublic
	case http.StatusForbidden, http.StatusTooManyRequests:
		// Unauthenticated API calls are limited to 60 an hour per address.
		return errors.New(lang.T().Err.RateLimited)
	default:
		return fmt.Errorf(lang.T().Err.GitHubQueryFmtS, resp.Status)
	}

	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf(lang.T().Err.GitHubUnreadableFmt, err)
	}
	return nil
}
