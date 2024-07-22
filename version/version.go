package version

// Version the library version number
const Version = "1.8.0"

// The build number
var Build string

func VersionString() string {
	if Build == "" {
		return Version + " dev"
	}
	return Version + " build " + Build
}
