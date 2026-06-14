package imageprovider

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWikiAPIResponseThumbnailDecode(t *testing.T) {
	t.Parallel()

	body := []byte(`{"query":{"pages":[{
		"title":"Turdus merula",
		"thumbnail":{"source":"https://upload.wikimedia.org/x.jpg","width":400,"height":300},
		"pageimage":"Common_Blackbird.jpg"
	}]}}`)

	var resp wikiAPIResponse
	require.NoError(t, json.Unmarshal(body, &resp))
	require.NotNil(t, resp.Query)
	require.Len(t, resp.Query.Pages, 1)

	page := resp.Query.Pages[0]
	require.NotNil(t, page.Thumbnail)
	assert.Equal(t, "https://upload.wikimedia.org/x.jpg", page.Thumbnail.Source)
	assert.Equal(t, "Common_Blackbird.jpg", page.PageImage)
}

func TestWikiAPIResponseAuthorDecode(t *testing.T) {
	t.Parallel()

	body := []byte(`{"query":{"pages":[{
		"title":"File:Common_Blackbird.jpg",
		"imageinfo":[{"extmetadata":{
			"Artist":{"value":"<a href=\"https://commons.wikimedia.org/wiki/User:JohnDoe\">John Doe</a>","source":"commons-desc-page"},
			"LicenseShortName":{"value":"CC BY-SA 4.0","source":"commons-desc-page"},
			"LicenseUrl":{"value":"https://creativecommons.org/licenses/by-sa/4.0","source":"commons-desc-page"}
		}}]
	}]}}`)

	var resp wikiAPIResponse
	require.NoError(t, json.Unmarshal(body, &resp))
	require.NotNil(t, resp.Query)
	require.Len(t, resp.Query.Pages, 1)
	require.Len(t, resp.Query.Pages[0].ImageInfo, 1)

	ext := resp.Query.Pages[0].ImageInfo[0].ExtMetadata
	require.NotEmpty(t, ext)
	assert.Equal(t, `<a href="https://commons.wikimedia.org/wiki/User:JohnDoe">John Doe</a>`, ext["Artist"].Value)
	assert.Equal(t, "CC BY-SA 4.0", ext["LicenseShortName"].Value)
	assert.Equal(t, "https://creativecommons.org/licenses/by-sa/4.0", ext["LicenseUrl"].Value)
}

func TestWikiAPIResponseErrorDecode(t *testing.T) {
	t.Parallel()

	body := []byte(`{"error":{"code":"missingtitle","info":"The page you specified doesn't exist."}}`)

	var resp wikiAPIResponse
	require.NoError(t, json.Unmarshal(body, &resp))
	assert.Nil(t, resp.Query)
	require.NotNil(t, resp.Error)
	assert.Equal(t, "missingtitle", resp.Error.Code)
}

func TestWikiAPIResponseMissingFields(t *testing.T) {
	t.Parallel()

	// Empty pages and missing optional fields must decode to zero values, not panic.
	var resp wikiAPIResponse
	require.NoError(t, json.Unmarshal([]byte(`{"query":{"pages":[]}}`), &resp))
	require.NotNil(t, resp.Query)
	assert.Empty(t, resp.Query.Pages)

	var page wikiPage
	require.NoError(t, json.Unmarshal([]byte(`{"title":"x"}`), &page))
	assert.Nil(t, page.Thumbnail)
	assert.Empty(t, page.PageImage)
	assert.Empty(t, page.ImageInfo)
}
