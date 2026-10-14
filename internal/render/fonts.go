package render

import (
	_ "embed"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

// Inter, SIL Open Font License 1.1 (see fonts/Inter-LICENSE.txt).
var (
	//go:embed fonts/Inter-Regular.ttf
	interRegular []byte
	//go:embed fonts/Inter-Medium.ttf
	interMedium []byte
	//go:embed fonts/Inter-SemiBold.ttf
	interSemiBold []byte
	//go:embed fonts/Inter-Bold.ttf
	interBold []byte
)

type weight int

const (
	regular weight = iota
	medium
	semibold
	bold
)

var (
	fontsOnce sync.Once
	fonts     [4]*opentype.Font
)

func loadFonts() {
	for i, data := range [][]byte{interRegular, interMedium, interSemiBold, interBold} {
		f, err := opentype.Parse(data)
		if err != nil {
			panic("render: parse embedded font: " + err.Error())
		}
		fonts[i] = f
	}
}

// faceCache creates font faces lazily for one render. Faces are not safe
// for concurrent use, so each render gets its own cache.
type faceCache map[[2]float64]font.Face

func (c faceCache) get(w weight, px float64) font.Face {
	fontsOnce.Do(loadFonts)
	key := [2]float64{float64(w), px}
	if f, ok := c[key]; ok {
		return f
	}
	f, err := opentype.NewFace(fonts[w], &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		panic("render: new face: " + err.Error())
	}
	c[key] = f
	return f
}

func (c faceCache) close() {
	for _, f := range c {
		f.Close()
	}
}
