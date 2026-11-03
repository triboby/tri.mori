package main

import (
	"os"
	"sync"
	"time"
)

type fileHit struct {
	book *Obj
	file *Obj
}

type listState struct {
	mtime  time.Time
	list   []any
	byID   map[string]*Obj
	byFile map[string]fileHit
}

// jsonList is a JSON array file that is re-read whenever its mtime changes.
type jsonList struct {
	file  string
	mu    sync.Mutex
	state *listState
}

func newJSONList(file string) *jsonList {
	return &jsonList{file: file, state: &listState{byID: map[string]*Obj{}}}
}

// mapKey distinguishes numbers from strings, as Map keys do.
func mapKey(v any) string {
	switch v.(type) {
	case float64, int, int64:
		return "n:" + jsString(v)
	case string:
		return "s:" + v.(string)
	}
	return "x:" + jsString(v)
}

func (l *jsonList) load() (*listState, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, err := os.Stat(l.file)
	if err != nil {
		return l.state, nil
	}
	if st.ModTime().Equal(l.state.mtime) && l.state.list != nil {
		return l.state, nil
	}
	data, err := os.ReadFile(l.file)
	if err != nil {
		return nil, err
	}
	v, err := parseJSON(data)
	if err != nil {
		return nil, err
	}
	list, _ := v.([]any)
	s := &listState{mtime: st.ModTime(), list: list, byID: map[string]*Obj{}}
	for _, x := range list {
		if o, ok := x.(*Obj); ok {
			s.byID[mapKey(o.Get("id"))] = o
		}
	}
	if len(list) > 0 {
		if first, ok := list[0].(*Obj); ok && truthy(first.Get("files")) {
			s.byFile = map[string]fileHit{}
			for _, x := range list {
				b, _ := x.(*Obj)
				if b == nil {
					continue
				}
				files, _ := b.Get("files").([]any)
				for _, f := range files {
					if fo, ok := f.(*Obj); ok {
						s.byFile[mapKey(fo.Get("id"))] = fileHit{b, fo}
					}
				}
			}
		}
	}
	l.state = s
	return s, nil
}

func strList(v any) []any {
	a, _ := v.([]any)
	return a
}
