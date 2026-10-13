package web

import (
	"regexp"
	"testing"
	"time"
)

func TestDraftStoreBounds(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	d := newDraftStore(func() time.Time { return now })

	id := d.save(&draft{session: "s1", form: formValues{Name: "a"}, html: []byte("x")})
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id) {
		t.Fatalf("id %q is not 128 random bits of hex", id)
	}
	if other := d.save(&draft{session: "s1"}); other == id {
		t.Fatal("ids must be unique")
	}

	t.Run("bound to its session", func(t *testing.T) {
		if _, ok := d.load(id, "s2"); ok {
			t.Fatal("another session must not read the draft")
		}
		if got, ok := d.load(id, "s1"); !ok || got.form.Name != "a" {
			t.Fatalf("load = %v %v", got, ok)
		}
	})

	t.Run("expires", func(t *testing.T) {
		now = now.Add(draftTTL - time.Second)
		if _, ok := d.load(id, "s1"); !ok {
			t.Fatal("draft must still be valid just before the TTL")
		}
		now = now.Add(2 * time.Second)
		if _, ok := d.load(id, "s1"); ok {
			t.Fatal("draft must expire after the TTL")
		}
		if d.len() != 0 {
			t.Fatalf("expired drafts are dropped, %d left", d.len())
		}
	})

	t.Run("per session cap evicts the oldest", func(t *testing.T) {
		var ids []string
		for i := 0; i < maxDraftsPerSession+1; i++ {
			now = now.Add(time.Second)
			ids = append(ids, d.save(&draft{session: "s3"}))
		}
		if _, ok := d.load(ids[0], "s3"); ok {
			t.Fatal("the oldest draft must be evicted")
		}
		if _, ok := d.load(ids[len(ids)-1], "s3"); !ok {
			t.Fatal("the newest draft must be kept")
		}
	})

	t.Run("global cap", func(t *testing.T) {
		for i := 0; i < maxDrafts*3; i++ {
			now = now.Add(time.Second)
			d.save(&draft{session: string(rune('a' + i%26))})
		}
		if d.len() > maxDrafts {
			t.Fatalf("%d drafts kept, cap is %d", d.len(), maxDrafts)
		}
	})

	t.Run("drop", func(t *testing.T) {
		id := d.save(&draft{session: "s9"})
		d.drop(id)
		if _, ok := d.load(id, "s9"); ok {
			t.Fatal("dropped draft is still there")
		}
	})
}
