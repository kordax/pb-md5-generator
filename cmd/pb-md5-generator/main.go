package main

import (
	"os"

	"github.com/kordax/pb-md5-generator/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args[1:]))
}
