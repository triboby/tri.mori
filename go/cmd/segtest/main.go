package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"movieshelf/jieba"
)

func main() {
	j := jieba.Default()
	in, _ := io.ReadAll(os.Stdin)
	for _, l := range strings.Split(string(in), "\n") {
		a, _ := json.Marshal(j.Cut(l, true))
		b, _ := json.Marshal(j.CutForSearch(l, true))
		fmt.Printf("%s\t%s\n", a, b)
	}
}
