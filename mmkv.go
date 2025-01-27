package mmkvgo

type MMKV struct {
	metaInfo *MetaInfo
	kv       map[string]int // key -> offset
}
