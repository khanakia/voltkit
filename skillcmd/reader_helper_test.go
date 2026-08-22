package skillcmd

import "bytes"

// bytesReader mirrors the tiny voltcfg helper: keeps test call sites tidy.
func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
