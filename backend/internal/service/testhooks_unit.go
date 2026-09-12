//go:build unit

package service

// SetCodexModelsURLForTest redirects cross-package probe tests to a local
// upstream. Tests using this process-global endpoint must not run in parallel.
func SetCodexModelsURLForTest(url string) (restore func()) {
	original := chatgptCodexModelsURL
	chatgptCodexModelsURL = url
	return func() { chatgptCodexModelsURL = original }
}
