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
	assert.Equal(t, `<a href="https://commons.wikimedia.org/wiki/User:JohnDoe">John Doe</a>`, extMetaString(ext, "Artist"))
	assert.Equal(t, "CC BY-SA 4.0", extMetaString(ext, "LicenseShortName"))
	assert.Equal(t, "https://creativecommons.org/licenses/by-sa/4.0", extMetaString(ext, "LicenseUrl"))
}

func TestWikiAPIResponseNumericExtMetadata(t *testing.T) {
	t.Parallel()

	// Real Wikimedia Commons extmetadata includes CommonsMetadataExtension whose
	// "value" is a JSON number (the extension version, e.g. 1.2), not a string.
	// The whole response must still decode and the string fields must still extract.
	body := []byte(`{"query":{"pages":[{
		"title":"File:Common_Blackbird.jpg",
		"imageinfo":[{"extmetadata":{
			"Artist":{"value":"John Doe","source":"commons-desc-page"},
			"LicenseShortName":{"value":"CC BY-SA 4.0","source":"commons-desc-page"},
			"CommonsMetadataExtension":{"value":1.2,"source":"extension"},
			"DateTime":{"value":"2020-01-01 00:00:00","source":"commons-desc-page"}
		}}]
	}]}}`)

	var resp wikiAPIResponse
	require.NoError(t, json.Unmarshal(body, &resp), "real extmetadata with numeric value must decode")
	require.NotNil(t, resp.Query)
	require.Len(t, resp.Query.Pages, 1)
	require.Len(t, resp.Query.Pages[0].ImageInfo, 1)

	ext := resp.Query.Pages[0].ImageInfo[0].ExtMetadata
	artist, _ := ext["Artist"].Value.(string)
	license, _ := ext["LicenseShortName"].Value.(string)
	assert.Equal(t, "John Doe", artist)
	assert.Equal(t, "CC BY-SA 4.0", license)
}

func TestWikiAPIResponseErrorDecode(t *testing.T) {
	t.Parallel()

	body := []byte(`{"error":{"code":"missingtitle","info":"The page you specified doesn't exist."}}`)

	var resp wikiAPIResponse
	require.NoError(t, json.Unmarshal(body, &resp))
	assert.Nil(t, resp.Query)
	require.NotNil(t, resp.Error)
	assert.Equal(t, "missingtitle", resp.Error.Code)
	assert.Equal(t, "The page you specified doesn't exist.", resp.Error.Info)
}

func TestWikiAPIResponseRedirectsNormalizedDecode(t *testing.T) {
	t.Parallel()

	// redirects/normalized are only used by len() in diagnostics, but the json
	// tags must stay correct so the counts are non-zero when present.
	body := []byte(`{"query":{
		"redirects":[{"from":"Parus caeruleus","to":"Cyanistes caeruleus"}],
		"normalized":[{"from":"parus","to":"Parus"}],
		"pages":[]
	}}`)

	var resp wikiAPIResponse
	require.NoError(t, json.Unmarshal(body, &resp))
	require.NotNil(t, resp.Query)
	assert.Len(t, resp.Query.Redirects, 1)
	assert.Len(t, resp.Query.Normalized, 1)
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
