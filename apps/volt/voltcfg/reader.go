package voltcfg

import "bytes"

// bytesReader is a tiny indirection so config.go reads top-down without an
// inline bytes.NewReader breaking the Load switch's flow.
func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
