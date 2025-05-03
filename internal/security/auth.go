package security

// AuthMethod represents the method used for authentication.
type AuthMethod string

const (
	AuthMethodNone        AuthMethod = ""       // No authentication used
	AuthMethodLocalSubnet AuthMethod = "subnet" // Authentication bypassed via local subnet access
	AuthMethodOAuth2      AuthMethod = "oauth2" // Authentication via OAuth2 token
	AuthMethodAPIKey      AuthMethod = "apikey" // Authentication via API Key
)

// SubnetUsername is a placeholder username for requests authenticated via subnet bypass.
const SubnetUsername = "<subnet>"
