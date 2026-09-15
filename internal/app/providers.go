package app

import "fmt"

// providers are the values a one-off `provider` override may take: the
// agents internal/app/wire.go knows how to build. The set lives here, in
// the layer both shells call, so a typo is refused the same way whether it
// arrives over HTTP or through the Wails bridge — the bridge binds the
// Service directly and never passes through internal/httpapi.
var providers = map[string]bool{
	"claude": true, "codex": true, "openai": true, "acp": true, "qwen": true, "agy": true,
}

// ProviderList names the set the way an error message should.
const ProviderList = "claude, codex, openai, acp, qwen or agy"

// ValidProvider reports whether name is a provider Sirdar drives. An empty
// name is the workspace's own provider and is always valid.
func ValidProvider(name string) bool {
	return name == "" || providers[name]
}

// CheckProvider turns an unknown provider into an ErrInvalidArgument, which
// every start funnels through so no job is begun over a typo.
func CheckProvider(name string) error {
	if ValidProvider(name) {
		return nil
	}
	return fmt.Errorf("%w: provider %q: must be %s", ErrInvalidArgument, name, ProviderList)
}
