package appicon

import (
	"bytes"
	"image/png"
	"testing"
)

func TestRenderShape(t *testing.T) {
	img := Render(128, Options{})
	if _, _, _, a := img.At(2, 2).RGBA(); a != 0 {
		t.Fatal("the corner outside the tile is transparent")
	}
	if _, _, _, a := img.At(64, 59).RGBA(); a != 0xffff {
		t.Fatal("the marble is opaque")
	}
	bare := Render(64, Options{Bare: true})
	if _, _, _, a := bare.At(32, 31).RGBA(); a != 0xffff {
		t.Fatal("the bare marble is opaque at its centre")
	}
	if _, _, _, a := bare.At(1, 1).RGBA(); a != 0 {
		t.Fatal("the bare marble has no tile")
	}
}

func TestColoursDiffer(t *testing.T) {
	seen := map[[3]uint32]string{}
	for _, n := range Names {
		r, g, b, _ := Render(48, Options{Colour: n, Bare: true}).At(30, 30).RGBA()
		k := [3]uint32{r >> 8, g >> 8, b >> 8}
		if other, dup := seen[k]; dup {
			t.Fatalf("%s and %s draw the same", n, other)
		}
		seen[k] = n
	}
	if len(Names) != len(Colours) {
		t.Fatal("every colour is offered")
	}
}

func TestPNG(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(PNG(64, Options{Colour: "Teal"})))
	if err != nil || img.Bounds().Dx() != 64 {
		t.Fatal(err)
	}
}
