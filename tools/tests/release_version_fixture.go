package main

import "fmt"

// This fixture deliberately has no VERSION artifact.
var Version = "stale-source-version"
var Commit, Date, BuildType string

func main() {
	fmt.Println(Version)
}
