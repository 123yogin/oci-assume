// Command rendertask writes the Windows task XML as UTF-16LE, for CI's real schtasks /Create check.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"unicode/utf16"

	"github.com/123yogin/oci-assume/internal/refresher"
)

func main() {
	bin := flag.String("bin", `C:\oci-assume\oci-assume-bg.exe`, "binary the task runs")
	out := flag.String("out", "task.xml", "output file")
	flag.Parse()
	files, err := refresher.Render("windows", *bin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	u := utf16.Encode([]rune(files["task.xml"]))
	b := make([]byte, 2+2*len(u))
	b[0], b[1] = 0xFF, 0xFE
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2+2*i:], c)
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
