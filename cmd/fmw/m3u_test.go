package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseM3U(t *testing.T) {
	const list = "\r\n#EXTM3U\r\n" +
		"#EXTINF:215,Kraftwerk - Autobahn\r\n" +
		`Kraftwerk\01 Autobahn.mp3` + "\r\n" +
		"# a comment\r\n" +
		"/music/abs.flac\r\n" +
		"#EXTINF:-1 tvg-id=\"x\",Radio\n" +
		"http://example.com/stream\n" +
		"file:///music/with%20space.ogg\n"
	got := parseM3U(strings.NewReader(list), "/lists")
	want := []entry{
		{Path: "/lists/Kraftwerk/01 Autobahn.mp3", Title: "Kraftwerk - Autobahn", Seconds: 215},
		{Path: "/music/abs.flac", Seconds: -1},
		{Path: "http://example.com/stream", Title: "Radio", Seconds: -1},
		{Path: "/music/with space.ogg", Seconds: -1},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parsed\n%v\nwant\n%v", got, want)
	}
}

func TestReadM3UWindows1252(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.m3u")
	if err := os.WriteFile(path, []byte("#EXTINF:100,Bj\xf6rk \x96 Army of Me\r\nBj\xf6rk\\army.mp3\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readM3U(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Björk – Army of Me" || got[0].Path != filepath.Join(dir, "Björk", "army.mp3") {
		t.Errorf("read %+v", got)
	}
}

func TestMediaIn(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b.mp3", "a.FLAC", "cover.jpg", "list.m3u", "cd2/c.ogg"} {
		path := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, nil, 0o644)
	}
	var got []string
	for _, e := range mediaIn(dir) {
		got = append(got, strings.TrimPrefix(e.Path, dir+string(filepath.Separator)))
	}
	if want := []string{"a.FLAC", "b.mp3", filepath.Join("cd2", "c.ogg")}; !slices.Equal(got, want) {
		t.Errorf("found %q, want %q", got, want)
	}
}

func TestWriteM3URoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "list.m3u8")
	list := []entry{
		{Path: filepath.Join(dir, "sub", "a.mp3"), Title: "A", Seconds: 61},
		{Path: "/elsewhere/b.mp3", Seconds: -1},
		{Path: "https://example.com/c", Title: "C", Seconds: -1},
	}
	if err := writeM3U(path, list); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "\nsub/a.mp3\n") {
		t.Errorf("a.mp3 not written relative:\n%s", b)
	}
	got, err := readM3U(path)
	if err != nil {
		t.Fatal(err)
	}
	list[1].Title = "b" // the name it was written with
	if !slices.Equal(got, list) {
		t.Errorf("read back\n%v\nwant\n%v", got, list)
	}
}
