package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// entry is a file or URL on a playlist, with the title and length in seconds
// a playlist file gave it, if any, -1 for an unknown length.
type entry struct {
	Path    string
	Title   string
	Seconds int
}

// name is what a playlist shows for e.
func (e entry) name() string {
	if e.Title != "" {
		return e.Title
	}
	if isURL(e.Path) {
		return e.Path
	}
	return strings.TrimSuffix(filepath.Base(e.Path), filepath.Ext(e.Path))
}

func isPlaylist(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".m3u" || ext == ".m3u8"
}

func isURL(s string) bool {
	i := strings.Index(s, "://")
	return i > 1 && !strings.ContainsAny(s[:i], `/\`)
}

// readM3U reads an M3U playlist, relative paths in it taken from the folder
// it is in. Old ones come from Windows, so a playlist that is not UTF-8 is
// read as Windows-1252 and backslashes separate folders everywhere.
func readM3U(path string) ([]entry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(b) {
		if b, err = charmap.Windows1252.NewDecoder().Bytes(b); err != nil {
			return nil, err
		}
	}
	return parseM3U(bytes.NewReader(b), filepath.Dir(path)), nil
}

func parseM3U(r io.Reader, dir string) []entry {
	var list []entry
	info := entry{Seconds: -1}
	s := bufio.NewScanner(r)
	s.Buffer(nil, 1<<20)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		switch {
		case line == "":
		case strings.HasPrefix(line, "#EXTINF:"):
			secs, title, _ := strings.Cut(strings.TrimPrefix(line, "#EXTINF:"), ",")
			if i := strings.IndexByte(secs, ' '); i >= 0 { // attributes, as in IPTV lists
				secs = secs[:i]
			}
			info.Title = strings.TrimSpace(title)
			if n, err := strconv.ParseFloat(strings.TrimSpace(secs), 64); err == nil && n >= 0 {
				info.Seconds = int(n)
			}
		case line[0] == '#':
		default:
			info.Path = resolve(line, dir)
			list = append(list, info)
			info = entry{Seconds: -1}
		}
	}
	return list
}

// resolve turns a playlist's line into a path or URL to open.
func resolve(line, dir string) string {
	if strings.HasPrefix(strings.ToLower(line), "file://") {
		if u, err := url.Parse(line); err == nil {
			line = u.Path
			if runtime.GOOS == "windows" {
				line = strings.TrimPrefix(line, "/")
			}
			return filepath.FromSlash(line)
		}
	}
	if isURL(line) {
		return line
	}
	if runtime.GOOS != "windows" {
		line = strings.ReplaceAll(line, `\`, "/")
	}
	if filepath.IsAbs(line) || filepath.VolumeName(line) != "" {
		return filepath.Clean(line)
	}
	return filepath.Join(dir, filepath.FromSlash(line))
}

// writeM3U writes list as an extended M3U playlist in UTF-8, with paths
// relative to the folder it goes in where they are inside it, so the folder
// can move with the playlist.
func writeM3U(path string, list []entry) error {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	dir := filepath.Dir(path)
	for _, e := range list {
		fmt.Fprintf(&b, "#EXTINF:%d,%s\n", e.Seconds, e.name())
		p := e.Path
		if rel, err := filepath.Rel(dir, p); err == nil && !isURL(p) && filepath.IsAbs(p) && filepath.IsLocal(rel) {
			p = filepath.ToSlash(rel)
			if strings.HasPrefix(p, "#") { // not a comment
				p = "./" + p
			}
		}
		b.WriteString(p + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
