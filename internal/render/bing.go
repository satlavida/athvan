package render

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"net/url"
	"strings"
	"sync"
	"time"
)

// BingImage is metadata about a Bing image of the day.
type BingImage struct {
	Title     string `json:"title" example:"Autumn in the Dolomites"`
	Copyright string `json:"copyright" example:"Seceda ridge, Italy (© Example/Getty Images)"`
	Date      string `json:"date" doc:"Bing start date, YYYYMMDD" example:"20261014"`
	URL       string `json:"url" doc:"Full-resolution image URL" example:"https://www.bing.com/th?id=OHR.Example_UHD.jpg"`
	Link      string `json:"link,omitempty" doc:"Bing's info page for the photo"`
}

type bingEntry struct {
	meta    BingImage
	img     image.Image
	fetched time.Time
}

var (
	bingMu    sync.Mutex
	bingCache = map[string]*bingEntry{}
)

const bingTTL = 30 * time.Minute

// BingMeta returns metadata for the Bing image of the day.
func BingMeta(ctx context.Context, market string, index int) (BingImage, error) {
	e, err := bing(ctx, market, index, false)
	if err != nil {
		return BingImage{}, err
	}
	return e.meta, nil
}

func bingImage(ctx context.Context, market string, index int) (image.Image, BingImage, error) {
	e, err := bing(ctx, market, index, true)
	if err != nil {
		return nil, BingImage{}, err
	}
	return e.img, e.meta, nil
}

func bing(ctx context.Context, market string, index int, withImage bool) (*bingEntry, error) {
	if market == "" {
		market = "en-US"
	}
	key := fmt.Sprintf("%s/%d", strings.ToLower(market), index)

	bingMu.Lock()
	e := bingCache[key]
	bingMu.Unlock()
	fresh := e != nil && time.Since(e.fetched) < bingTTL
	if fresh && (!withImage || e.img != nil) {
		return e, nil
	}

	if !fresh {
		meta, err := fetchBingMeta(ctx, market, index)
		if err != nil {
			return nil, err
		}
		e = &bingEntry{meta: meta, fetched: time.Now()}
	}
	if withImage && e.img == nil {
		img, err := fetchBingPhoto(ctx, e.meta.URL)
		if err != nil {
			return nil, err
		}
		e = &bingEntry{meta: e.meta, img: img, fetched: e.fetched}
	}
	bingMu.Lock()
	bingCache[key] = e
	bingMu.Unlock()
	return e, nil
}

func fetchBingMeta(ctx context.Context, market string, index int) (BingImage, error) {
	q := url.Values{"format": {"js"}, "idx": {fmt.Sprint(index)}, "n": {"1"}, "mkt": {market}}
	data, err := fetch(ctx, "https://www.bing.com/HPImageArchive.aspx?"+q.Encode())
	if err != nil {
		return BingImage{}, fmt.Errorf("bing: %w", err)
	}
	var body struct {
		Images []struct {
			StartDate     string `json:"startdate"`
			URLBase       string `json:"urlbase"`
			Title         string `json:"title"`
			Copyright     string `json:"copyright"`
			CopyrightLink string `json:"copyrightlink"`
		} `json:"images"`
	}
	if err := json.Unmarshal(data, &body); err != nil || len(body.Images) == 0 {
		return BingImage{}, fmt.Errorf("bing: unexpected response")
	}
	im := body.Images[0]
	return BingImage{
		Title:     im.Title,
		Copyright: im.Copyright,
		Date:      im.StartDate,
		// UHD is 3840x2160; enough to cover-crop a portrait phone screen.
		URL:  "https://www.bing.com" + im.URLBase + "_UHD.jpg",
		Link: im.CopyrightLink,
	}, nil
}

func fetchBingPhoto(ctx context.Context, uhdURL string) (image.Image, error) {
	data, err := fetch(ctx, uhdURL)
	if err != nil {
		// Not every market/day has a UHD rendition.
		data, err = fetch(ctx, strings.TrimSuffix(uhdURL, "_UHD.jpg")+"_1920x1080.jpg")
		if err != nil {
			return nil, fmt.Errorf("bing: %w", err)
		}
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("bing: decode: %w", err)
	}
	return img, nil
}
