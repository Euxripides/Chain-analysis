package crosschain

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func fixtureAttestation(t *testing.T, id string) CCTPAttestation {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + id + "-circle.json")
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		SourceTxHash string `json:"sourceTxHash"`
	}
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Query().Get("transactionHash") != data.SourceTxHash {
			t.Error("source transaction not bound to request")
		}
		w.Write(raw)
	}))
	defer srv.Close()
	c, _ := primaryFixtureLogs(t, id, "source")
	s := New(Config{Bases: map[string]string{"cctp": srv.URL}})
	messages, err := s.CCTPAttestations(context.Background(), c, data.SourceTxHash)
	if err != nil || len(messages) != 1 {
		t.Fatalf("authority association %s: %v %+v", id, err, messages)
	}
	return messages[0]
}

func TestCCTPRealSourceAndExecutedReceipt(t *testing.T) {
	for _, id := range []string{"B04", "B05", "B12", "B14", "B15", "B29", "B36"} {
		t.Run(id, func(t *testing.T) {
			att := fixtureAttestation(t, id)
			c, logs := primaryFixtureLogs(t, id, "source")
			d, dlogs := primaryFixtureLogs(t, id, "destination")
			sent, received, mints := 0, 0, 0
			for _, l := range logs {
				if _, ok := CCTPSourceMatches(c, l, att); ok {
					sent++
				}
			}
			for _, l := range dlogs {
				if _, ok := CCTPReceivedMatches(d, l, att); ok {
					received++
				}
				if CCTPMintEvent(d, l, att) {
					mints++
				}
			}
			if sent != 1 || received != 1 || mints != 1 {
				t.Fatalf("real CCTP events: sent=%d received=%d mint=%d", sent, received, mints)
			}
		})
	}
}

func TestCCTPRejectsChangedSourceMeaningAndOtherTransaction(t *testing.T) {
	att := fixtureAttestation(t, "B04")
	c, logs := primaryFixtureLogs(t, "B04", "source")
	for _, l := range logs {
		if _, ok := CCTPSourceMatches(c, l, att); !ok {
			continue
		}
		wrong := att
		wrong.SourceTxHash = "0x" + strings.Repeat("9", 64)
		if _, ok := CCTPSourceMatches(c, l, wrong); ok {
			t.Fatal("other source hash accepted")
		}
		raw, _ := hex.DecodeString(att.Message[2:])
		for _, offset := range []int{4, 8, 63, 95, 120, 143, 155, 185, 217, 249, 281} {
			altered := append([]byte(nil), raw...)
			altered[offset] ^= 1
			wrong = att
			wrong.Message = "0x" + hex.EncodeToString(altered)
			if _, ok := CCTPSourceMatches(c, l, wrong); ok {
				t.Fatalf("changed immutable message byte %d accepted", offset)
			}
		}
		l.Removed = true
		if _, ok := CCTPSourceMatches(c, l, att); ok {
			t.Fatal("removed source accepted")
		}
	}
}

func TestHashEchoCannotStopOtherProtocolLookups(t *testing.T) {
	h := "0x" + strings.Repeat("1", 64)
	if completeProtocolPair([]Transfer{{Source: []Leg{{Chain: "ethereum", TxHash: h}}, Destination: []Leg{{Chain: "base", TxHash: h}}}}) {
		t.Fatal("unverified hash echo stopped discovery")
	}
}
