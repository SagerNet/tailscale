// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

// Code generated by tailscale/cmd/viewer; DO NOT EDIT.

package drive

import (
	"encoding/json"
	"errors"

	"github.com/sagernet/tailscale/types/views"
)

//go:generate go run tailscale.com/cmd/cloner  -clonefunc=true -type=Share

// View returns a readonly view of Share.
func (p *Share) View() ShareView {
	return ShareView{ж: p}
}

// ShareView provides a read-only view over Share.
//
// Its methods should only be called if `Valid()` returns true.
type ShareView struct {
	// ж is the underlying mutable value, named with a hard-to-type
	// character that looks pointy like a pointer.
	// It is named distinctively to make you think of how dangerous it is to escape
	// to callers. You must not let callers be able to mutate it.
	ж *Share
}

// Valid reports whether underlying value is non-nil.
func (v ShareView) Valid() bool { return v.ж != nil }

// AsStruct returns a clone of the underlying value which aliases no memory with
// the original.
func (v ShareView) AsStruct() *Share {
	if v.ж == nil {
		return nil
	}
	return v.ж.Clone()
}

func (v ShareView) MarshalJSON() ([]byte, error) { return json.Marshal(v.ж) }

func (v *ShareView) UnmarshalJSON(b []byte) error {
	if v.ж != nil {
		return errors.New("already initialized")
	}
	if len(b) == 0 {
		return nil
	}
	var x Share
	if err := json.Unmarshal(b, &x); err != nil {
		return err
	}
	v.ж = &x
	return nil
}

func (v ShareView) Name() string { return v.ж.Name }
func (v ShareView) Path() string { return v.ж.Path }
func (v ShareView) As() string   { return v.ж.As }
func (v ShareView) BookmarkData() views.ByteSlice[[]byte] {
	return views.ByteSliceOf(v.ж.BookmarkData)
}

// A compilation failure here means this code must be regenerated, with the command at the top of this file.
var _ShareViewNeedsRegeneration = Share(struct {
	Name         string
	Path         string
	As           string
	BookmarkData []byte
}{})
