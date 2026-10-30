package api

import (
	"crypto/rand"
	"net/http"
	"strconv"
	"strings"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// msgSettingsPreconditionFailed is the client message for a settings write whose
// If-Match no longer matches the current settings.
const msgSettingsPreconditionFailed = "Settings changed since they were loaded; reload them and try again"

// settingsETagEpoch identifies this process. It is part of every settings ETag,
// so an ETag issued before a restart never matches one issued after it, when the
// revision counter starts over. crypto/rand.Text uses an alphabet without quotes
// or commas, so it is safe inside an ETag.
var settingsETagEpoch = rand.Text()

// settingsETag returns the strong ETag of a settings snapshot, in the form
// "<process epoch>-<revision>". It names the client-writable state of the
// snapshot (see conf.Settings.Revision). The runtime range filter fields keep the
// same ETag when they change, because clients cannot write them.
func settingsETag(s *conf.Settings) string {
	return `"` + settingsETagEpoch + "-" + strconv.FormatUint(s.Revision(), 10) + `"`
}

// checkSettingsPrecondition returns nil when the request carried no If-Match
// header or its If-Match matches the ETag of current, and a 412 rejection
// carrying the current ETag otherwise. A header that is present but empty lists
// no entity tag, so it never matches and is refused rather than read as "no
// precondition". It is meant to be the first statement of a commitSettings mutate
// function, where current is the snapshot the write is applied to, so the check
// is atomic with the publish. ifMatch and present come from requestIfMatch, read
// before commitSettings so the mutate does no request I/O under the writer lock.
func checkSettingsPrecondition(ifMatch string, present bool, current *conf.Settings) error {
	if !present {
		return nil
	}
	etag := settingsETag(current)
	if apicore.MatchIfMatch(ifMatch, etag) {
		return nil
	}
	return newPreconditionRejection(etag)
}

// requestIfMatch returns the If-Match value of a request: every If-Match header
// line joined into one comma-separated list (RFC 9110 section 5.3). present
// reports whether the request sent the header at all, so an empty header can be
// told from an absent one. Header.Get would return only the first line.
func requestIfMatch(r *http.Request) (ifMatch string, present bool) {
	values := r.Header.Values(apicore.HeaderIfMatch)
	return strings.Join(values, ","), len(values) > 0
}
