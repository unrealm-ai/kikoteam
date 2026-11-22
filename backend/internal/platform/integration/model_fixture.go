package integration

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math/big"
)

type modelImageFixture struct{ Data, Color, Code string }

func newModelImageFixture() (modelImageFixture, error) {
	colors := []struct {
		Name string
		RGB  color.RGBA
	}{{"red", color.RGBA{230, 20, 30, 255}}, {"blue", color.RGBA{20, 50, 230, 255}}, {"green", color.RGBA{20, 170, 40, 255}}, {"yellow", color.RGBA{245, 220, 10, 255}}}
	idx, err := rand.Int(rand.Reader, big.NewInt(4))
	if err != nil {
		return modelImageFixture{}, err
	}
	num, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return modelImageFixture{}, err
	}
	chosen := colors[idx.Int64()]
	code := fmt.Sprintf("%04d", num.Int64())
	img := image.NewRGBA(image.Rect(0, 0, 96, 88))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(32, 16, 64, 48), image.NewUniform(chosen.RGB), image.Point{}, draw.Src)
	glyphs := []string{"111101101101111", "010110010010111", "111001111100111", "111001111001111", "101101111001001", "111100111001111", "111100111101111", "111001001001001", "111101111101111", "111101111001111"}
	for i, digit := range code {
		g := glyphs[digit-'0']
		for y := range 5 {
			for x := range 3 {
				if g[y*3+x] == '1' {
					draw.Draw(img, image.Rect(25+i*12+x*3, 61+y*3, 28+i*12+x*3, 64+y*3), image.NewUniform(color.Black), image.Point{}, draw.Src)
				}
			}
		}
	}
	var b bytes.Buffer
	if err = png.Encode(&b, img); err != nil {
		return modelImageFixture{}, err
	}
	return modelImageFixture{base64.StdEncoding.EncodeToString(b.Bytes()), chosen.Name, code}, nil
}
