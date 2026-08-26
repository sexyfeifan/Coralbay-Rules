package main

import (
	"github.com/sexyfeifan/coralbay-rules/internal/regions"
	"log"
	"os"
)

func main() {
	for _, p := range []string{"templates/ppanel_openclash_pro_cn.gotmpl", "templates/openclash/Pro_cn.upstream.yaml", "templates/subconverter/mihomopro.ini"} {
		b, e := os.ReadFile(p)
		if e != nil {
			log.Fatal(e)
		}
		if e = os.WriteFile(p, []byte(regions.Rewrite(string(b))), 0644); e != nil {
			log.Fatal(e)
		}
	}
}
