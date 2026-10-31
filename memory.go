package main

import "runtime/debug"

// releaseMemory returns freed memory to the system straight away, after a
// big one-off job such as decoding a wallpaper or a background picture.
func releaseMemory() { debug.FreeOSMemory() }
