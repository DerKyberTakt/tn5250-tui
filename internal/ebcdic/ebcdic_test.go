package ebcdic

import "testing"

func TestCodePage37(t *testing.T) {
	cp := MustGet(37)
	if got := cp.DecodeBytes([]byte{0xc8, 0x85, 0x93, 0x93, 0x96}); got != "Hello" {
		t.Fatalf("decode = %q", got)
	}
	if got := cp.EncodeString("Hello"); string(got) != string([]byte{0xc8, 0x85, 0x93, 0x93, 0x96}) {
		t.Fatalf("encode = %x", got)
	}
	if cp.Decode(0x00) != ' ' || cp.Decode(0x20) != ' ' || cp.Decode(0x3f) != ' ' {
		t.Fatal("nulls and attributes must decode to a space")
	}
	if cp.Decode(0x01) != Unprintable || cp.Decode(0xff) != Unprintable {
		t.Fatal("control bytes must decode to the hollow box")
	}
	if cp.Encode('€') != Substitute {
		t.Fatal("the euro sign is not in code page 37")
	}
	if MustGet(1140).Encode('€') == Substitute {
		t.Fatal("the euro sign is in code page 1140")
	}
	if _, err := Get(9999); err == nil {
		t.Fatal("an unknown page must fail")
	}
	if len(Supported()) < 10 || Supported()[0] != 37 {
		t.Fatalf("supported = %v", Supported())
	}
}
