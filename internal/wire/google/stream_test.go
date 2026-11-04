package google

import (
	"bytes"
	"strings"
	"testing"
)

const sseStream = "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Looking\"}]},\"index\":0}],\"usageMetadata\":{\"promptTokenCount\":900}}\r\n\r\n" +
	"data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[" +
	"{\"functionCall\":{\"name\":\"compress\",\"args\":{\"ranges\":[{\"startRef\":\"m00002\",\"endRef\":\"m00005\",\"summary\":\"done\"}]}},\"thoughtSignature\":\"s\"}," +
	"{\"functionCall\":{\"name\":\"read_file\",\"args\":{\"path\":\"a.go\"}}}]},\"finishReason\":\"STOP\",\"index\":0}]," +
	"\"usageMetadata\":{\"promptTokenCount\":900,\"candidatesTokenCount\":42,\"totalTokenCount\":942,\"cachedContentTokenCount\":512}}\r\n\r\n" +
	"data: not json\n\n" +
	"data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"decompress\",\"args\":{\"blockId\":\"b3\",\"mode\":\"peek\"}}}]},\"index\":0}]}\n\n"

func TestStreamPassthroughCopiesBytes(t *testing.T) {
	var dst bytes.Buffer
	sum, err := StreamPassthrough(&dst, strings.NewReader(sseStream))
	if err != nil {
		t.Fatal(err)
	}
	if dst.String() != sseStream {
		t.Errorf("stream not copied byte for byte")
	}
	if !sum.HasUsage || sum.Usage.PromptTokens != 900 || sum.Usage.CachedTokens != 512 || sum.Usage.CompletionTokens != 42 {
		t.Errorf("usage %+v has=%v", sum.Usage, sum.HasUsage)
	}
	if !sum.Complete {
		t.Error("finishReason not seen")
	}
}

func TestStreamPassthroughCalls(t *testing.T) {
	var dst bytes.Buffer
	sum, err := StreamPassthrough(&dst, strings.NewReader(sseStream))
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Calls) != 2 {
		t.Fatalf("calls %d, want 2 (read_file ignored): %+v", len(sum.Calls), sum.Calls)
	}
	c := sum.Calls[0]
	if c.Name != ToolCompress || c.Part != 1 || c.Err != nil || c.Compress == nil {
		t.Fatalf("compress call %+v", c)
	}
	if c.Compress.Ranges[0].StartSeq != 2 || c.Compress.Ranges[0].EndSeq != 5 || c.Compress.Ranges[0].Summary != "done" {
		t.Errorf("compress args %+v", c.Compress)
	}
	d := sum.Calls[1]
	if d.Name != ToolDecompress || d.Part != 3 || d.Decompress == nil || d.Decompress.BlockID != "b3" || d.Decompress.Mode != "peek" {
		t.Errorf("decompress call %+v", d)
	}
}

func TestStreamPassthroughBadArgsDoNotStop(t *testing.T) {
	in := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"compress\",\"args\":{\"ranges\":[]}}}]},\"index\":0}]}\n\n" +
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"after\"}]},\"finishReason\":\"STOP\",\"index\":0}]}\n\n"
	var dst bytes.Buffer
	sum, err := StreamPassthrough(&dst, strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if dst.String() != in || len(sum.Calls) != 1 || sum.Calls[0].Err == nil || !sum.Complete {
		t.Errorf("sum %+v", sum)
	}
}
