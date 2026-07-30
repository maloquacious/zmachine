package zmachine

import (
	"errors"
	"testing"
)

// Bytes planted in the test story so that reads can be identified. 0x033f is
// the last byte of dynamic memory and 0x0340 the first byte of static memory.
const (
	testLastDynamicByte = 0xab
	testFirstStaticByte = 0xcd
	testLastStoryByte   = 0xef
	testLastDynamicAddr = 0x033f
	testFirstStaticAddr = 0x0340
	testStorySize       = 0x0400
	testHighBaseAddr    = 0x0360
)

// newTestStory builds the standard fixture with identifiable bytes at the
// dynamic/static boundary and at the end of the image.
func newTestStory(t *testing.T) *Story {
	t.Helper()

	data := validTestHeader().build()
	data[testLastDynamicAddr] = testLastDynamicByte
	data[testFirstStaticAddr] = testFirstStaticByte
	data[testStorySize-1] = testLastStoryByte

	story, err := LoadStory(data)
	if err != nil {
		t.Fatalf("LoadStory() error = %v, want nil", err)
	}
	return story
}

func TestMemoryReadByteBoundaries(t *testing.T) {
	m := newMemory(newTestStory(t))

	tests := []struct {
		name    string
		addr    uint32
		want    uint8
		wantErr bool
	}{
		{name: "first byte of the header is the version", addr: 0x0000, want: versionV3},
		{name: "last byte of dynamic memory", addr: testLastDynamicAddr, want: testLastDynamicByte},
		{name: "first byte of static memory", addr: testFirstStaticAddr, want: testFirstStaticByte},
		{name: "last byte of the story", addr: testStorySize - 1, want: testLastStoryByte},
		{name: "one byte past the end", addr: testStorySize, wantErr: true},
		{name: "far past the end", addr: 0xffff, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m.readByte(tt.addr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("readByte(0x%04x) = 0x%02x, want an error", tt.addr, got)
				}
				assertMemoryError(t, err, MemoryRead, tt.addr, RegionUnknown)
				return
			}
			if err != nil {
				t.Fatalf("readByte(0x%04x) error = %v, want nil", tt.addr, err)
			}
			if got != tt.want {
				t.Errorf("readByte(0x%04x) = 0x%02x, want 0x%02x", tt.addr, got, tt.want)
			}
		})
	}
}

// TestMemoryReadWordSpansDynamicStaticBoundary checks that a word read is not
// confined to one region: dynamic and static memory are contiguous in the
// address space (S 1.1) even though the interpreter stores them separately.
func TestMemoryReadWordSpansDynamicStaticBoundary(t *testing.T) {
	m := newMemory(newTestStory(t))

	got, err := m.readWord(testLastDynamicAddr)
	if err != nil {
		t.Fatalf("readWord(0x%04x) error = %v, want nil", uint32(testLastDynamicAddr), err)
	}
	// S 2.1: words are stored most significant byte first.
	const want = uint16(testLastDynamicByte)<<8 | uint16(testFirstStaticByte)
	if got != want {
		t.Errorf("readWord(0x%04x) = 0x%04x, want 0x%04x", uint32(testLastDynamicAddr), got, want)
	}
}

func TestMemoryReadWordBoundaries(t *testing.T) {
	m := newMemory(newTestStory(t))

	if _, err := m.readWord(testStorySize - 2); err != nil {
		t.Errorf("readWord at the last word of the story error = %v, want nil", err)
	}
	// The second byte of this word lies past the end of the story.
	if _, err := m.readWord(testStorySize - 1); err == nil {
		t.Errorf("readWord(0x%04x) succeeded, want an error", uint32(testStorySize-1))
	} else {
		var memErr *MemoryError
		if errors.As(err, &memErr) && memErr.Width != 2 {
			t.Errorf("MemoryError.Width = %d, want 2", memErr.Width)
		}
	}
}

// TestMemoryWritesOutsideDynamicMemoryFail covers S 1.1.2: it is illegal to
// write to static memory, and high memory cannot be accessed directly at all.
func TestMemoryWritesOutsideDynamicMemoryFail(t *testing.T) {
	tests := []struct {
		name       string
		addr       uint32
		wantRegion Region
	}{
		{name: "first byte of static memory", addr: testFirstStaticAddr, wantRegion: RegionStatic},
		{name: "inside high memory", addr: testHighBaseAddr, wantRegion: RegionHigh},
		{name: "last byte of the story", addr: testStorySize - 1, wantRegion: RegionHigh},
		{name: "past the end of the story", addr: testStorySize, wantRegion: RegionUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			story := newTestStory(t)
			m := newMemory(story)

			err := m.writeByte(tt.addr, 0x5a)
			if err == nil {
				t.Fatalf("writeByte(0x%04x) = nil, want an error", tt.addr)
			}
			assertMemoryError(t, err, MemoryWrite, tt.addr, tt.wantRegion)

			if tt.addr < uint32(len(story.image)) && story.image[tt.addr] == 0x5a {
				t.Errorf("story image byte at 0x%04x was modified by a refused write", tt.addr)
			}
			if err := m.writeWord(tt.addr, 0x5a5a); err == nil {
				t.Errorf("writeWord(0x%04x) = nil, want an error", tt.addr)
			}
		})
	}
}

// TestMemoryWriteWordStraddlingStaticBoundaryIsRefusedEntirely checks that a
// partially illegal word write changes nothing: the dynamic half must not be
// applied before the static half is rejected.
func TestMemoryWriteWordStraddlingStaticBoundaryIsRefusedEntirely(t *testing.T) {
	m := newMemory(newTestStory(t))

	err := m.writeWord(testLastDynamicAddr, 0x1234)
	if err == nil {
		t.Fatalf("writeWord(0x%04x) = nil, want an error", uint32(testLastDynamicAddr))
	}
	assertMemoryError(t, err, MemoryWrite, testLastDynamicAddr, RegionDynamic)

	got, err := m.readByte(testLastDynamicAddr)
	if err != nil {
		t.Fatalf("readByte error = %v, want nil", err)
	}
	if got != testLastDynamicByte {
		t.Errorf("byte at 0x%04x = 0x%02x after a refused word write, want 0x%02x",
			uint32(testLastDynamicAddr), got, uint8(testLastDynamicByte))
	}
}

func TestMemoryWriteAndReadDynamicMemory(t *testing.T) {
	story := newTestStory(t)
	m := newMemory(story)

	if err := m.writeByte(0x0100, 0x7f); err != nil {
		t.Fatalf("writeByte error = %v, want nil", err)
	}
	if got, err := m.readByte(0x0100); err != nil || got != 0x7f {
		t.Errorf("readByte(0x0100) = 0x%02x, %v; want 0x7f, nil", got, err)
	}

	// S 2.1: the high byte is stored first.
	if err := m.writeWord(0x0102, 0xbeef); err != nil {
		t.Fatalf("writeWord error = %v, want nil", err)
	}
	if got, err := m.readByte(0x0102); err != nil || got != 0xbe {
		t.Errorf("high byte = 0x%02x, %v; want 0xbe, nil", got, err)
	}
	if got, err := m.readByte(0x0103); err != nil || got != 0xef {
		t.Errorf("low byte = 0x%02x, %v; want 0xef, nil", got, err)
	}
	if got, err := m.readWord(0x0102); err != nil || got != 0xbeef {
		t.Errorf("readWord(0x0102) = 0x%04x, %v; want 0xbeef, nil", got, err)
	}

	// The immutable story image must not have changed.
	if story.image[0x0100] == 0x7f {
		t.Error("write through a machine's memory reached the shared story image")
	}

	// The last writable address is the byte before the base of static memory.
	if err := m.writeByte(testLastDynamicAddr, 0x01); err != nil {
		t.Errorf("writeByte at the top of dynamic memory error = %v, want nil", err)
	}
	// A word write must fit entirely in dynamic memory.
	if err := m.writeWord(testLastDynamicAddr-1, 0x0102); err != nil {
		t.Errorf("writeWord at the top of dynamic memory error = %v, want nil", err)
	}
}

// TestMemoryHeaderIsWritable records that this layer permits header writes.
// The interpreter itself must set several header fields after loading
// (S 11.1); the rules about which fields a story may change belong to the
// instructions that write memory.
func TestMemoryHeaderIsWritable(t *testing.T) {
	m := newMemory(newTestStory(t))

	if err := m.writeByte(hdrFlags1, 0x20); err != nil {
		t.Fatalf("writeByte(header flags 1) error = %v, want nil", err)
	}
	if got, err := m.readByte(hdrFlags1); err != nil || got != 0x20 {
		t.Errorf("flags 1 = 0x%02x, %v; want 0x20, nil", got, err)
	}
}

// TestMemoryMachinesAreIsolated is the isolation contract: two machines built
// from one Story share no mutable state.
func TestMemoryMachinesAreIsolated(t *testing.T) {
	story := newTestStory(t)
	first := newMemory(story)
	second := newMemory(story)

	const addr = 0x0080
	before, err := second.readByte(addr)
	if err != nil {
		t.Fatalf("readByte error = %v, want nil", err)
	}

	if err := first.writeByte(addr, before+1); err != nil {
		t.Fatalf("writeByte error = %v, want nil", err)
	}
	if err := first.writeWord(0x00c0, 0x1234); err != nil {
		t.Fatalf("writeWord error = %v, want nil", err)
	}

	if got, err := second.readByte(addr); err != nil || got != before {
		t.Errorf("second machine sees 0x%02x at 0x%04x (err %v), want the untouched 0x%02x", got, uint32(addr), err, before)
	}
	if got, err := second.readWord(0x00c0); err != nil || got == 0x1234 {
		t.Errorf("second machine sees 0x%04x at 0x00c0 (err %v), want the untouched value", got, err)
	}

	// A third machine created after the writes must still start from the
	// story's initial dynamic memory.
	third := newMemory(story)
	if got, err := third.readByte(addr); err != nil || got != before {
		t.Errorf("new machine sees 0x%02x at 0x%04x (err %v), want the initial 0x%02x", got, uint32(addr), err, before)
	}
}

// TestMemorySharesStaticMemory checks that read-only memory is borrowed from
// the Story rather than copied per machine (specification S 35).
func TestMemorySharesStaticMemory(t *testing.T) {
	story := newTestStory(t)
	first := newMemory(story)
	second := newMemory(story)

	if &first.static[0] != &story.image[story.staticBase] {
		t.Error("static memory was copied instead of shared with the story")
	}
	if &first.static[0] != &second.static[0] {
		t.Error("two machines hold different copies of static memory")
	}
	if &first.dynamic[0] == &second.dynamic[0] {
		t.Error("two machines share dynamic memory")
	}
	if len(first.dynamic) != int(story.staticBase) {
		t.Errorf("dynamic memory is %d bytes, want the base of static memory %d", len(first.dynamic), story.staticBase)
	}
	if first.size() != story.length {
		t.Errorf("addressable size = %d, want the story length %d", first.size(), story.length)
	}
}

func TestMemoryRegionOf(t *testing.T) {
	m := newMemory(newTestStory(t))

	tests := []struct {
		addr uint32
		want Region
	}{
		{addr: 0x0000, want: RegionDynamic},
		{addr: testLastDynamicAddr, want: RegionDynamic},
		{addr: testFirstStaticAddr, want: RegionStatic},
		{addr: testHighBaseAddr - 1, want: RegionStatic},
		{addr: testHighBaseAddr, want: RegionHigh},
		{addr: testStorySize - 1, want: RegionHigh},
		{addr: testStorySize, want: RegionUnknown},
	}
	for _, tt := range tests {
		if got := m.regionOf(tt.addr); got != tt.want {
			t.Errorf("regionOf(0x%04x) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}

// TestUnpackAddress covers S 1.2.3: in Versions 1 to 3 the byte address of a
// routine or string is twice its packed address.
func TestUnpackAddress(t *testing.T) {
	tests := []struct {
		packed uint16
		want   uint32
	}{
		{packed: 0x0000, want: 0x00000},
		{packed: 0x0001, want: 0x00002},
		{packed: 0x2000, want: 0x04000},
		{packed: 0x8000, want: 0x10000},
		{packed: 0xffff, want: 0x1fffe},
	}
	for _, tt := range tests {
		if got := unpackAddress(tt.packed); got != tt.want {
			t.Errorf("unpackAddress(0x%04x) = 0x%05x, want 0x%05x", tt.packed, got, tt.want)
		}
	}
}

// TestExpandWordAddress covers S 1.2.2: a word address names an even byte
// address in the bottom 128K by giving it divided by two.
func TestExpandWordAddress(t *testing.T) {
	tests := []struct {
		word uint16
		want uint32
	}{
		{word: 0x0000, want: 0x00000},
		{word: 0x0021, want: 0x00042},
		{word: 0xffff, want: 0x1fffe},
	}
	for _, tt := range tests {
		if got := expandWordAddress(tt.word); got != tt.want {
			t.Errorf("expandWordAddress(0x%04x) = 0x%05x, want 0x%05x", tt.word, got, tt.want)
		}
	}
}

func TestOffsetAddress(t *testing.T) {
	tests := []struct {
		name    string
		base    uint32
		offset  int32
		want    uint32
		wantErr bool
	}{
		{name: "forwards", base: 0x0100, offset: 0x20, want: 0x0120},
		{name: "backwards", base: 0x0100, offset: -0x20, want: 0x00e0},
		{name: "to zero", base: 0x0010, offset: -0x10, want: 0x0000},
		{name: "below zero", base: 0x0010, offset: -0x11, wantErr: true},
		{name: "largest legal address", base: addressSpaceLimit - 2, offset: 1, want: addressSpaceLimit - 1},
		{name: "past the address space", base: addressSpaceLimit - 1, offset: 1, wantErr: true},
		{name: "far past the address space", base: 0xffff_ffff, offset: 1, wantErr: true},
		{name: "large negative offset", base: 0, offset: -0x7fff_ffff, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := offsetAddress(tt.base, tt.offset)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("offsetAddress(0x%04x, %d) = 0x%05x, want an error", tt.base, tt.offset, got)
				}
				if !errors.Is(err, ErrMemoryAccess) {
					t.Errorf("errors.Is(err, ErrMemoryAccess) = false; err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("offsetAddress(0x%04x, %d) error = %v, want nil", tt.base, tt.offset, err)
			}
			if got != tt.want {
				t.Errorf("offsetAddress(0x%04x, %d) = 0x%05x, want 0x%05x", tt.base, tt.offset, got, tt.want)
			}
		})
	}
}

// assertMemoryError checks the classification and context carried by a refused
// memory access.
func assertMemoryError(t *testing.T, err error, op MemoryOp, addr uint32, region Region) {
	t.Helper()

	if !errors.Is(err, ErrMemoryAccess) {
		t.Errorf("errors.Is(err, ErrMemoryAccess) = false; err = %v", err)
	}
	var memErr *MemoryError
	if !errors.As(err, &memErr) {
		t.Fatalf("errors.As(err, *MemoryError) = false; err = %v", err)
	}
	if memErr.Op != op {
		t.Errorf("MemoryError.Op = %v, want %v", memErr.Op, op)
	}
	if memErr.Addr != addr {
		t.Errorf("MemoryError.Addr = 0x%04x, want 0x%04x", memErr.Addr, addr)
	}
	if memErr.Region != region {
		t.Errorf("MemoryError.Region = %v, want %v", memErr.Region, region)
	}
	if memErr.Detail == "" {
		t.Error("MemoryError.Detail is empty; errors must carry context")
	}
}
