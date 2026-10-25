package dash

import (
	"net/url"
	"testing"
)

const mpdTemplate = `<?xml version="1.0"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static" mediaPresentationDuration="PT0H0M20.0S">
  <BaseURL>https://cdn.example.com/bbb/</BaseURL>
  <Period>
    <AdaptationSet mimeType="video/mp4" contentType="video">
      <SegmentTemplate timescale="1000" duration="4000" startNumber="1" initialization="$RepresentationID$/init.mp4" media="$RepresentationID$/seg-$Number%05d$.m4s"/>
      <Representation id="v720" bandwidth="2000000" width="1280" height="720" codecs="avc1.4d401f"/>
      <Representation id="v1080" bandwidth="5000000" width="1920" height="1080" codecs="avc1.640028"/>
    </AdaptationSet>
    <AdaptationSet mimeType="audio/mp4" lang="en">
      <Representation id="a128" bandwidth="128000" codecs="mp4a.40.2">
        <SegmentTemplate timescale="48000" initialization="a/init.mp4" media="a/$Time$.m4s">
          <SegmentTimeline>
            <S t="0" d="96000" r="2"/>
            <S d="48000"/>
          </SegmentTimeline>
        </SegmentTemplate>
      </Representation>
    </AdaptationSet>
  </Period>
</MPD>`

func TestParseTemplate(t *testing.T) {
	base, _ := url.Parse("https://origin.example.com/manifest.mpd")
	reps, err := ParseMPD(mpdTemplate, base)
	if err != nil {
		t.Fatal(err)
	}
	videos, audios := Videos(reps), Audios(reps)
	if len(videos) != 2 || len(audios) != 1 {
		t.Fatalf("got %d videos %d audios", len(videos), len(audios))
	}
	v := videos[0]
	if v.ID != "v1080" || v.InitURL != "https://cdn.example.com/bbb/v1080/init.mp4" {
		t.Fatalf("video rep: %+v", v)
	}
	if len(v.Segments) != 5 || v.Segments[0] != "https://cdn.example.com/bbb/v1080/seg-00001.m4s" || v.Segments[4] != "https://cdn.example.com/bbb/v1080/seg-00005.m4s" {
		t.Fatalf("segments: %v", v.Segments)
	}
	a := audios[0]
	if len(a.Segments) != 4 || a.Segments[3] != "https://cdn.example.com/bbb/a/288000.m4s" || a.Lang != "en" {
		t.Fatalf("audio timeline: %v lang=%q", a.Segments, a.Lang)
	}
	if SelectVideo(videos, "720p").ID != "v720" || SelectVideo(videos, "best").ID != "v1080" || SelectVideo(videos, "480").ID != "v720" {
		t.Fatal("SelectVideo")
	}
	if v.Label() != "1080p" || a.Label() != "128kbps" {
		t.Fatalf("labels %q %q", v.Label(), a.Label())
	}
}

func TestTimelineNegativeRepeatAndSegmentList(t *testing.T) {
	doc := `<MPD mediaPresentationDuration="PT10S"><Period>
	<AdaptationSet mimeType="video/mp4">
	  <SegmentTemplate timescale="1" media="s$Number$.m4s" startNumber="0"><SegmentTimeline><S t="0" d="2" r="-1"/></SegmentTimeline></SegmentTemplate>
	  <Representation id="r" bandwidth="1" width="640" height="360"/>
	</AdaptationSet>
	<AdaptationSet mimeType="audio/mp4">
	  <Representation id="l" bandwidth="1">
	    <SegmentList><Initialization sourceURL="ai.mp4"/><SegmentURL media="a1.m4s"/><SegmentURL media="a2.m4s"/></SegmentList>
	  </Representation>
	</AdaptationSet>
	</Period></MPD>`
	base, _ := url.Parse("https://h/p/m.mpd")
	reps, err := ParseMPD(doc, base)
	if err != nil {
		t.Fatal(err)
	}
	v := Videos(reps)[0]
	if len(v.Segments) != 5 || v.Segments[0] != "https://h/p/s0.m4s" {
		t.Fatalf("r=-1 expansion: %v", v.Segments)
	}
	a := Audios(reps)[0]
	if a.InitURL != "https://h/p/ai.mp4" || len(a.Segments) != 2 {
		t.Fatalf("segment list: %+v", a)
	}
}

func TestSegmentBaseAndDRM(t *testing.T) {
	base, _ := url.Parse("https://h/m.mpd")
	reps, err := ParseMPD(`<MPD><Period><AdaptationSet mimeType="video/webm"><Representation id="x" bandwidth="9"><BaseURL>video.webm</BaseURL><SegmentBase indexRange="0-100"/></Representation></AdaptationSet></Period></MPD>`, base)
	if err != nil || len(reps) != 1 || reps[0].SingleURL != "https://h/video.webm" || reps[0].Units() != 1 {
		t.Fatalf("segment base: %v %+v", err, reps)
	}
	_, err = ParseMPD(`<MPD><Period><AdaptationSet mimeType="video/mp4"><ContentProtection schemeIdUri="urn:uuid:x"/><Representation id="x" bandwidth="1"><BaseURL>v.mp4</BaseURL></Representation></AdaptationSet></Period></MPD>`, base)
	if err != ErrDRM {
		t.Fatalf("expected ErrDRM, got %v", err)
	}
}

func TestParseDuration(t *testing.T) {
	if d := ParseDuration("PT1H2M3.5S"); d != 3723.5 {
		t.Fatalf("got %v", d)
	}
	if d := ParseDuration("P1DT1S"); d != 86401 {
		t.Fatalf("got %v", d)
	}
	if fill("$RepresentationID$/$Number%05d$-$Bandwidth$$$", "r", 7, 0, 99) != "r/00007-99$" {
		t.Fatal("fill")
	}
}
