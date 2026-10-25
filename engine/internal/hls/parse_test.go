package hls

import (
	"net/url"
	"testing"
)

const master = `#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",LANGUAGE="en",DEFAULT=YES,URI="audio/en.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=426x240,CODECS="avc1.42c01e,mp4a.40.2",AUDIO="aud"
video_10_0.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=12000000,RESOLUTION=3840x2160,CODECS="avc1.640033,mp4a.40.2",FRAME-RATE=25.000,AUDIO="aud"
video_10_5.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1280x720,AUDIO="aud"
video_10_3.m3u8
`

const media = `#EXTM3U
#EXT-X-VERSION:6
#EXT-X-TARGETDURATION:10
#EXT-X-MEDIA-SEQUENCE:5
#EXT-X-MAP:URI="init.mp4",BYTERANGE="720@0"
#EXT-X-KEY:METHOD=AES-128,URI="key.bin",IV=0x0000000000000000000000000000002A
#EXTINF:9.009,
#EXT-X-BYTERANGE:1000@2000
seg.m4s
#EXTINF:9.009,
#EXT-X-BYTERANGE:1000
seg.m4s
#EXT-X-KEY:METHOD=NONE
#EXTINF:4.5,
https://cdn.example.com/other.m4s
#EXT-X-ENDLIST
`

func TestParseMaster(t *testing.T) {
	base, _ := url.Parse("https://vod.aryzap.com/a/b/adp.10.m3u8")
	vs, rs, err := ParseMaster(master, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 3 || len(rs) != 1 {
		t.Fatalf("got %d variants %d renditions", len(vs), len(rs))
	}
	if vs[1].URL != "https://vod.aryzap.com/a/b/video_10_5.m3u8" || vs[1].Height != 2160 || vs[1].FrameRate != 25 {
		t.Fatalf("variant parse wrong: %+v", vs[1])
	}
	if vs[0].Codecs != "avc1.42c01e,mp4a.40.2" {
		t.Fatalf("quoted codecs with comma mis-parsed: %q", vs[0].Codecs)
	}
	if rs[0].URL != "https://vod.aryzap.com/a/b/audio/en.m3u8" || !rs[0].Default {
		t.Fatalf("rendition wrong: %+v", rs[0])
	}
	if got := SelectVariant(vs, "best"); got.Height != 2160 {
		t.Fatalf("best → %d", got.Height)
	}
	if got := SelectVariant(vs, "1080p"); got.Height != 720 {
		t.Fatalf("1080p cap → %d", got.Height)
	}
	if got := SelectVariant(vs, "worst"); got.Height != 240 {
		t.Fatalf("worst → %d", got.Height)
	}
	if PickAudio(rs, "aud") == nil || PickAudio(rs, "none") != nil {
		t.Fatal("PickAudio")
	}
}

func TestParseMedia(t *testing.T) {
	base, _ := url.Parse("https://cdn.example.com/v/index.m3u8")
	pl, err := ParseMedia(media, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Segments) != 3 || !pl.EndList || !pl.FMP4 || pl.TargetDuration != 10 {
		t.Fatalf("playlist: %+v", pl)
	}
	s0, s1, s2 := pl.Segments[0], pl.Segments[1], pl.Segments[2]
	if s0.Seq != 5 || s1.Seq != 6 || s0.Duration != 9.009 {
		t.Fatalf("sequence/duration wrong: %+v %+v", s0, s1)
	}
	if s0.Range == nil || s0.Range.Offset != 2000 || s0.Range.Length != 1000 {
		t.Fatalf("explicit byterange: %+v", s0.Range)
	}
	if s1.Range == nil || s1.Range.Offset != 3000 {
		t.Fatalf("implicit byterange offset should continue previous: %+v", s1.Range)
	}
	if s0.Key == nil || s0.Key.Method != "AES-128" || s0.Key.URI != "https://cdn.example.com/v/key.bin" || s0.Key.IV[15] != 0x2A {
		t.Fatalf("key: %+v", s0.Key)
	}
	if s2.Key != nil {
		t.Fatal("METHOD=NONE should clear the key")
	}
	if s0.MapURL != "https://cdn.example.com/v/init.mp4" || s0.MapRange == nil || s0.MapRange.Length != 720 {
		t.Fatalf("map: %q %+v", s0.MapURL, s0.MapRange)
	}
	if pl.DRM() || !pl.Encrypted() || pl.Duration() < 22 {
		t.Fatalf("flags: drm=%v enc=%v dur=%v", pl.DRM(), pl.Encrypted(), pl.Duration())
	}
}

func TestDRMDetection(t *testing.T) {
	pl, _ := ParseMedia("#EXTM3U\n#EXT-X-KEY:METHOD=SAMPLE-AES,URI=\"skd://x\"\n#EXTINF:4,\na.ts\n", nil)
	if !pl.DRM() {
		t.Fatal("SAMPLE-AES must be flagged as DRM")
	}
}

func TestDecryptCBC(t *testing.T) {
	key := []byte("0123456789abcdef")
	iv := make([]byte, 16)
	// 16 bytes of plaintext + full PKCS7 padding block, encrypted with the same key/iv.
	plain := []byte("hello hls world!")
	enc := encryptForTest(plain, key, iv)
	got, err := decryptCBC(enc, key, iv)
	if err != nil || string(got) != string(plain) {
		t.Fatalf("got %q err %v", got, err)
	}
}
