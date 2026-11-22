// Command hermes-icon writes the app icon as PNG files:
//
//	go run ./cmd/hermes-icon [-size 1024] [-colour Pink] [-o assets/icon.png] [-sheet assets/icon-colours.png]
package main

import (
	"flag"
	"image"
	"image/draw"
	"image/png"
	"log"
	"os"

	"hermes-desktop/internal/appicon"
)

func main() {
	size := flag.Int("size", 1024, "pixels")
	colour := flag.String("colour", "Pink", "marble colour")
	out := flag.String("o", "assets/icon.png", "output PNG")
	sheet := flag.String("sheet", "", "also write every colour side by side here")
	flag.Parse()
	write := func(path string, img image.Image) {
		f, err := os.Create(path)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			log.Fatal(err)
		}
	}
	write(*out, appicon.Render(*size, appicon.Options{Colour: *colour}))
	if *sheet != "" {
		const s = 160
		all := image.NewRGBA(image.Rect(0, 0, s*len(appicon.Names), s))
		for i, n := range appicon.Names {
			draw.Draw(all, image.Rect(i*s, 0, (i+1)*s, s), appicon.Render(s, appicon.Options{Colour: n}), image.Point{}, draw.Over)
		}
		write(*sheet, all)
	}
}
