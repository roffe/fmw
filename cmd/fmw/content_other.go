//go:build !android

package main

func contentName(string) string { return "" }

func keepContent(string) {}
