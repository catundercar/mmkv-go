package mmkvgo

import (
	"path"
	"syscall"
	"testing"
)

func testNewKVFile(t *testing.T) *KVFile {
	fpath := path.Join("test", "data")

	DEFAULT_MMAP_SIZE = uint64(syscall.Getpagesize())
	t.Logf("DEFAULT_MMAP_SIZE: %d\n", DEFAULT_MMAP_SIZE)
	memoryFile, err := NewMMKVMemoryFile(path.Join(fpath, "mmkv.default"), DEFAULT_MMAP_SIZE, true)
	if err != nil {
		t.Fatalf("Failed to create memory file: %v", err)
	}
	defer memoryFile.Close()
	err = memoryFile.ReloadFromFile(DEFAULT_MMAP_SIZE)
	if err != nil {
		t.Fatalf("Failed to reload from file: %v", err)
	}
	t.Logf("load memfile length: %v", len(memoryFile.data))

	kvfile, err := ReloadKVFile(memoryFile)
	if err != nil {
		t.Fatalf("Failed to read meta info: %v", err)
	}
	return kvfile
}

func TestListKeys(t *testing.T) {
	kvfile := testNewKVFile(t)
	t.Log(kvfile.ListKeys())
}

func TestKVFile_GetInt32(t *testing.T) {
	kvfile := testNewKVFile(t)

	type args struct {
		key string
	}
	tests := []struct {
		name    string
		args    args
		want    int32
		wantErr bool
	}{
		{
			name:    "GetInt32",
			args:    args{key: "int32"},
			wantErr: false,
			want:    22,
		},
		{
			name:    "GetInt32 NotFound",
			args:    args{key: "not found"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := kvfile.GetInt32(tt.args.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetInt32() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetInt32() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKVFile_GetUInt32(t *testing.T) {
	kvfile := testNewKVFile(t)

	type args struct {
		key string
	}
	tests := []struct {
		name    string
		args    args
		want    uint32
		wantErr bool
	}{
		{
			name:    "GetUInt32",
			args:    args{key: "uint32"},
			wantErr: false,
			want:    22,
		},
		{
			name:    "GetUInt32 NotFound",
			args:    args{key: "not found"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := kvfile.GetUInt32(tt.args.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetInt32() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetInt32() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKVFile_GetInt64(t *testing.T) {
	kvfile := testNewKVFile(t)

	type args struct {
		key string
	}
	tests := []struct {
		name    string
		args    args
		want    int64
		wantErr bool
	}{
		{
			name:    "GetInt64",
			args:    args{key: "int64"},
			wantErr: false,
			want:    -22,
		},
		{
			name:    "GetInt64 NotFound",
			args:    args{key: "not found"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := kvfile.GetInt64(tt.args.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetInt64() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetInt64() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKVFile_GetUInt64(t *testing.T) {
	kvfile := testNewKVFile(t)

	type args struct {
		key string
	}
	tests := []struct {
		name    string
		args    args
		want    uint64
		wantErr bool
	}{
		{
			name:    "GetUInt64",
			args:    args{key: "uint64"},
			wantErr: false,
			want:    22,
		},
		{
			name:    "GetUInt64 NotFound",
			args:    args{key: "not found"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := kvfile.GetUInt64(tt.args.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetUInt64() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetUInt64() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKVFile_GetFloat32(t *testing.T) {
	kvfile := testNewKVFile(t)

	type args struct {
		key string
	}
	tests := []struct {
		name    string
		args    args
		want    float32
		wantErr bool
	}{
		{
			name:    "GetFloat32",
			args:    args{key: "float32"},
			wantErr: false,
			want:    22.22,
		},
		{
			name:    "GetFloat32 NotFound",
			args:    args{key: "not found"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := kvfile.GetFloat32(tt.args.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetFloat32() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetFloat32() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKVFile_GetFloat64(t *testing.T) {
	kvfile := testNewKVFile(t)

	type args struct {
		key string
	}
	tests := []struct {
		name    string
		args    args
		want    float64
		wantErr bool
	}{
		{
			name:    "GetFloat64",
			args:    args{key: "float64"},
			wantErr: false,
			want:    22.22,
		},
		{
			name:    "GetFloat64 NotFound",
			args:    args{key: "not found"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := kvfile.GetFloat64(tt.args.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetFloat64() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetFloat64() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKVFile_GetString(t *testing.T) {
	kvfile := testNewKVFile(t)

	type args struct {
		key string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name:    "GetString",
			args:    args{key: "hello"},
			wantErr: false,
			want:    "world",
		},
		{
			name:    "GetString NotFound",
			args:    args{key: "not found"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := kvfile.GetString(tt.args.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetString() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetString() got = %v, want %v", got, tt.want)
			}
		})
	}
}
