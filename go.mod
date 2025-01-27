module catundercar.github.com/mmkv-go

go 1.23.2

replace (
	tencent.com/mmkv => ./third_party/mmkv
	unlock-music.dev/mmkv => ../go-mmkv
)

require (
	google.golang.org/protobuf v1.33.0
	tencent.com/mmkv v0.0.0
	unlock-music.dev/mmkv v0.1.0
)

require golang.org/x/exp v0.0.0-20240416160154-fe59bbe5cc7f // indirect
