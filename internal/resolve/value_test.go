package resolve

import (
	"reflect"
	"testing"
)

func TestParseValue(t *testing.T) {
	v, err := ParseValue("aws-1.0;rank=20;src=pvc:spack/nccl/1;mount=/opt/modules/aws/1.0;lib=/a:/b;mask=/m")
	if err != nil {
		t.Fatal(err)
	}
	want := ModuleValue{Name: "aws-1.0", Rank: 20, Src: "pvc:spack/nccl/1", Mount: "/opt/modules/aws/1.0", Lib: []string{"/a", "/b"}, Mask: []string{"/m"}}
	if !reflect.DeepEqual(v, want) {
		t.Errorf("got %+v", v)
	}
	for _, bad := range []string{"", "x;rank=a;src=image;mount=/m", "x;src=image", "x;rank=10;junk;mount=/m"} {
		if _, err := ParseValue(bad); err == nil {
			t.Errorf("ParseValue(%q) must fail", bad)
		}
	}
}
