package google

import "testing"

func TestParseCompressArgsReadsFolds(t *testing.T) {
	in := []byte(`{"ranges":[{"startRef":"m00001","endRef":"m00004","summary":"s"}],"folds":[{"tier":2,"startBlock":"b3","endBlock":"b5","summary":"fold text","topic":"t"},{"tier":"T3","startBlock":"b9","endBlock":"b11","summary":"x"}],"closeIntents":[7]}`)
	args, err := ParseCompressArgs(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(args.Ranges) != 1 || len(args.CloseIntents) != 1 || args.CloseIntents[0] != 7 {
		t.Fatalf("ranges or closeIntents lost: %+v", args)
	}
	if len(args.Folds) != 2 {
		t.Fatalf("folds = %+v, want 2", args.Folds)
	}
	if f := args.Folds[0]; f.Tier != 2 || f.StartBlock != "b3" || f.EndBlock != "b5" || f.Summary != "fold text" || f.Topic != "t" {
		t.Fatalf("fold 0 = %+v", f)
	}
	if f := args.Folds[1]; f.Tier != 3 || f.StartBlock != "b9" || f.EndBlock != "b11" {
		t.Fatalf("fold 1 = %+v", f)
	}
}

func TestParseCompressArgsRejectsBadFolds(t *testing.T) {
	for _, in := range []string{
		`{"ranges":[{"startRef":"m00001","endRef":"m00004","summary":"s"}],"folds":"nope"}`,
		`{"ranges":[{"startRef":"m00001","endRef":"m00004","summary":"s"}],"folds":[{"startBlock":"b1","endBlock":"b2","summary":"s"}]}`,
		`{"ranges":[{"startRef":"m00001","endRef":"m00004","summary":"s"}],"folds":[{"tier":"deep","startBlock":"b1","endBlock":"b2","summary":"s"}]}`,
	} {
		if _, err := ParseCompressArgs([]byte(in)); err == nil {
			t.Fatalf("expected error for %s", in)
		}
	}
}

func TestParseCompressArgsWithoutFoldsLeavesThemNil(t *testing.T) {
	args, err := ParseCompressArgs([]byte(`{"ranges":[{"startRef":"m00001","endRef":"m00004","summary":"s"}]}`))
	if err != nil || args.Folds != nil {
		t.Fatalf("args %+v err %v", args, err)
	}
}
