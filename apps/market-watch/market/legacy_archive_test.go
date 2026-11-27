package market

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

func TestLegacyBundleVerifiesRawDigestAndOffset(t *testing.T) {
	raw := []byte("<html><body>exact public response</body></html>")
	var gz bytes.Buffer
	z := gzip.NewWriter(&gz)
	_, _ = z.Write(raw)
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	pack := func(name string) []byte {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		if e := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(gz.Len()), Typeflag: tar.TypeReg}); e != nil {
			t.Fatal(e)
		}
		_, _ = tw.Write(gz.Bytes())
		if e := tw.Close(); e != nil {
			t.Fatal(e)
		}
		return buf.Bytes()
	}
	good := pack(sampleHash(raw) + ".gz")
	rows, e := parseLegacyBundle(good)
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	r := rows[0]
	if !r.HTML || r.OriginalBytes != int64(len(raw)) || !bytes.Equal(good[r.Offset:r.Offset+r.Length], gz.Bytes()) {
		t.Fatal(r)
	}
	if _, e = parseLegacyBundle(pack(sampleHash([]byte("other")) + ".gz")); e == nil {
		t.Fatal("incorrect raw accepted")
	}
	if _, e = parseLegacyBundle(pack("../" + sampleHash(raw) + ".gz")); e == nil {
		t.Fatal("invalid entry name accepted")
	}
}
