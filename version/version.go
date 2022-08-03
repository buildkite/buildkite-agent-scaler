package version

// Version the library version number
const Version = "1.3.2"

// The build number
var Build string

func VersionString() string {
	if Build == "" {
		return Version + " dev"
	}
	return Version + " build " + Build
}
