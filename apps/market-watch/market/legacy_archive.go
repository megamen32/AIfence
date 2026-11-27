package market

import (
	"agent/internal/db"
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/airlockrun/agentsdk"
)

const maxLegacyBundle = 12 << 20

type LegacyRawEntry struct {
	Hash          string `json:"sha256"`
	Offset        int64  `json:"offset"`
	Length        int64  `json:"length"`
	HTML          bool   `json:"html"`
	OriginalBytes int64  `json:"-"`
}
type LegacyRawReceipt struct {
	Source   string `json:"source"`
	Snapshot string `json:"snapshot"`
	Digest   string `json:"sha256"`
	Objects  int    `json:"objects"`
	Updated  int64  `json:"updated_responses"`
	Bytes    int    `json:"bytes"`
}

func parseLegacyBundle(blob []byte) ([]LegacyRawEntry, error) {
	if len(blob) > maxLegacyBundle {
		return nil, fmt.Errorf("bundle exceeds limit")
	}
	rd := bytes.NewReader(blob)
	tr := tar.NewReader(rd)
	var entries []LegacyRawEntry
	seen := map[string]bool{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if len(entries) >= 512 || h.Typeflag != tar.TypeReg || h.Size <= 0 || h.Size > maxLegacyBundle {
			return nil, fmt.Errorf("invalid bundle entry")
		}
		hash := strings.TrimSuffix(h.Name, ".gz")
		if h.Name != hash+".gz" || !validHash(hash) || seen[hash] {
			return nil, fmt.Errorf("invalid or duplicate raw hash")
		}
		offset := int64(len(blob) - rd.Len())
		compressed, e := io.ReadAll(io.LimitReader(tr, h.Size+1))
		if e != nil || int64(len(compressed)) != h.Size {
			return nil, fmt.Errorf("truncated raw member")
		}
		if offset < 0 || offset+h.Size > int64(len(blob)) || !bytes.Equal(compressed, blob[offset:offset+h.Size]) {
			return nil, fmt.Errorf("invalid archive offset")
		}
		z, e := gzip.NewReader(bytes.NewReader(compressed))
		if e != nil {
			return nil, e
		}
		raw, e := io.ReadAll(io.LimitReader(z, (32<<20)+1))
		ce := z.Close()
		if e != nil || ce != nil || len(raw) > 32<<20 || sampleHash(raw) != hash {
			return nil, fmt.Errorf("original response digest mismatch: %s", hash)
		}
		prefix := strings.ToLower(string(raw[:min(len(raw), 4096)]))
		html := strings.Contains(prefix, "<html") || strings.Contains(prefix, "<!doctype html") || strings.Contains(prefix, "<body")
		entries = append(entries, LegacyRawEntry{Hash: hash, Offset: offset, Length: h.Size, HTML: html, OriginalBytes: int64(len(raw))})
		seen[hash] = true
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("empty archive bundle")
	}
	return entries, nil
}
func (s *Service) StageLegacyArchive(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	source, snapshot, digest := r.URL.Query().Get("source"), r.URL.Query().Get("snapshot"), r.Header.Get("X-Archive-SHA256")
	src, e := legacySource(source)
	if e != nil || !validHash(snapshot) || !validHash(digest) || s.conn == nil {
		return agentsdk.NewHTTPError(400, "invalid legacy archive identity", nil)
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLegacyBundle)
	defer r.Body.Close()
	blob, e := io.ReadAll(r.Body)
	if e != nil || sampleHash(blob) != digest {
		return agentsdk.NewHTTPError(400, "archive bundle digest or size mismatch", nil)
	}
	entries, e := parseLegacyBundle(blob)
	if e != nil {
		return agentsdk.NewHTTPError(400, e.Error(), nil)
	}
	id := adapterID(src.Host)
	meta, e := s.queries.AdaptiveStoreMeta(ctx, id)
	if e != nil {
		return e
	}
	var old AdaptiveStore
	if e = json.Unmarshal(meta.Metadata, &old); e != nil {
		return e
	}
	if old.LegacySource != source || old.MigrationSnapshot != snapshot || old.Status != "migration_staged" || old.ActiveVersion != "" {
		return agentsdk.NewHTTPError(409, "source is not the expected staged snapshot", nil)
	}
	hashes := make([]string, 0, len(entries))
	for _, x := range entries {
		hashes = append(hashes, x.Hash)
	}
	refs, e := s.queries.LegacyRawReferences(ctx, db.LegacyRawReferencesParams{StoreID: id, Hashes: hashes})
	if e != nil {
		return e
	}
	seen := map[string]int64{}
	for _, ref := range refs {
		if prior, ok := seen[ref.Sha256]; ok && prior != ref.OriginalBytes {
			return fmt.Errorf("inconsistent original response size")
		}
		seen[ref.Sha256] = ref.OriginalBytes
	}
	for _, x := range entries {
		if n, ok := seen[x.Hash]; !ok || n != x.OriginalBytes {
			return agentsdk.NewHTTPError(409, "archive object does not match staged receipt: "+x.Hash, nil)
		}
	}
	// One native file write per bundle, not per page. Validate its read-back
	// checksum before publishing offsets into Postgres.
	path := adapterPath(id) + "/legacy/" + digest + ".tar"
	current, e := s.store.ReadFile(ctx, path)
	if e == nil {
		if sampleHash(current) != digest {
			return fmt.Errorf("existing immutable archive bundle mismatch")
		}
	} else {
		if !errors.Is(e, agentsdk.ErrNotFound) {
			return e
		}
		if _, e = s.store.WriteFile(ctx, path, bytes.NewReader(blob), "application/x-tar"); e != nil {
			return e
		}
		current, e = s.store.ReadFile(ctx, path)
		if e != nil || sampleHash(current) != digest {
			return fmt.Errorf("archive read-back checksum failed")
		}
	}
	raw, e := json.Marshal(entries)
	if e != nil {
		return e
	}
	var updated int64
	e = s.conn.Transaction(ctx, nil, func(tx agentsdk.DBTX) error {
		q := db.New(tx)
		b, e := q.LockMigrationStore(ctx, id)
		if e != nil {
			return e
		}
		var a AdaptiveStore
		if e = json.Unmarshal(b, &a); e != nil {
			return e
		}
		if a.MigrationSnapshot != snapshot || a.LegacySource != source || a.Status != "migration_staged" {
			return fmt.Errorf("staged source changed during archive write")
		}
		updated, e = q.StoreLegacyRawReceipts(ctx, db.StoreLegacyRawReceiptsParams{Path: path, StoreID: id, Entries: raw})
		return e
	})
	if e != nil {
		return e
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(LegacyRawReceipt{source, snapshot, digest, len(entries), updated, len(blob)})
}
