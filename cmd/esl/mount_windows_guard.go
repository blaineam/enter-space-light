//go:build windows && !cmount

package main

// Fails the build with a clear message instead of producing an esl.exe that
// can't mount anything.
var _ = windowsBuildsNeedTheCmountTag_UseGoBuildTagsCmount
