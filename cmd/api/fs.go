package main

import (
	"net/http"
	"os"
)

// noDirListFS wraps an http.FileSystem and returns os.ErrNotExist for any
// directory Open, preventing the default file-listing page on /static/ etc.
type noDirListFS struct{ http.FileSystem }

func (n noDirListFS) Open(name string) (http.File, error) {
	f, err := n.FileSystem.Open(name)
	if err != nil {
		return nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if stat.IsDir() {
		f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}
