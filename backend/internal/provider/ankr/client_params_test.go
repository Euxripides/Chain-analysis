package ankr

import (
	"reflect"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

func TestBuildParamsUsesExactlyOneRangeDimension(t *testing.T) {
	client := &Client{}
	fromBlock, toBlock := uint64(100), uint64(200)
	fromTime := time.UnixMilli(1_700_000_000_000).UTC()
	toTime := fromTime.Add(time.Hour)

	t.Run("time capable dataset prefers timestamps", func(t *testing.T) {
		params := client.buildParams(provider.FetchRequest{
			Chain: "bsc", Dataset: "transactions", Address: "0x1",
			FromBlock: &fromBlock, ToBlock: &toBlock, FromTime: &fromTime, ToTime: &toTime,
		})
		if _, ok := params["fromBlock"]; ok {
			t.Fatalf("fromBlock must be omitted when fromTimestamp is used: %#v", params)
		}
		if _, ok := params["toBlock"]; ok {
			t.Fatalf("toBlock must be omitted when toTimestamp is used: %#v", params)
		}
		if params["fromTimestamp"] != fromTime.UnixMilli() || params["toTimestamp"] != toTime.UnixMilli() {
			t.Fatalf("timestamp bounds missing: %#v", params)
		}
	})

	t.Run("block only request keeps block bounds", func(t *testing.T) {
		params := client.buildParams(provider.FetchRequest{Chain: "bsc", Dataset: "transactions", Address: "0x1", FromBlock: &fromBlock, ToBlock: &toBlock})
		if params["fromBlock"] != "0x64" || params["toBlock"] != "0xc8" {
			t.Fatalf("block bounds missing: %#v", params)
		}
		if _, ok := params["fromTimestamp"]; ok {
			t.Fatalf("unexpected timestamp bound: %#v", params)
		}
	})

	t.Run("dataset without time support keeps block bounds", func(t *testing.T) {
		params := client.buildParams(provider.FetchRequest{
			Chain: "bsc", Dataset: "logs", Address: "0x1",
			FromBlock: &fromBlock, ToBlock: &toBlock, FromTime: &fromTime, ToTime: &toTime,
		})
		if params["fromBlock"] != "0x64" || params["toBlock"] != "0xc8" {
			t.Fatalf("logs must retain block bounds: %#v", params)
		}
		if _, ok := params["fromTimestamp"]; ok {
			t.Fatalf("logs must not send unsupported timestamps: %#v", params)
		}
	})
}

func TestBuildParamsSendsTopicsOnlyForLogs(t *testing.T) {
	client := &Client{}
	topics := [][]string{{"0x2f87", "0xf6391f"}}
	if params := client.buildParams(provider.FetchRequest{Chain: "bsc", Dataset: "logs", Address: "0x1", Topics: topics}); !reflect.DeepEqual(params["topics"], topics) {
		t.Fatalf("logs topics missing: %#v", params)
	}
	for _, req := range []provider.FetchRequest{{Chain: "bsc", Dataset: "logs", Address: "0x1"}, {Chain: "bsc", Dataset: "transactions", Address: "0x1", Topics: topics}} {
		if _, ok := client.buildParams(req)["topics"]; ok {
			t.Fatalf("unexpected topics for %#v", req)
		}
	}
}
