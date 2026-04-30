package keeper

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBitmap(t *testing.T) {
	bh := NewBitmapHelper()

	specs := map[string]struct {
		n       int
		expSize int
	}{
		"zero validators":         {n: 0, expSize: 0},
		"one validator":           {n: 1, expSize: 1},
		"seven validators":        {n: 7, expSize: 1},
		"eight validators":        {n: 8, expSize: 1},
		"nine validators":         {n: 9, expSize: 2},
		"sixteen validators":      {n: 16, expSize: 2},
		"seventeen validators":    {n: 17, expSize: 3},
		"one-hundred validators":  {n: 100, expSize: 13},
		"ten-thousand validators": {n: 10_000, expSize: 1250},
	}

	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			bitmap := bh.NewBitmap(spec.n)
			require.Len(t, bitmap, spec.expSize)
			for _, b := range bitmap {
				assert.Equal(t, byte(0), b, "new bitmap must be zero-initialised")
			}
		})
	}
}

func TestSetBitAndIsSet(t *testing.T) {
	bh := NewBitmapHelper()

	t.Run("set and read within range", func(t *testing.T) {
		bitmap := bh.NewBitmap(16)
		// Cover low-byte, high-byte, and edges.
		for _, idx := range []int{0, 1, 7, 8, 9, 15} {
			assert.False(t, bh.IsSet(bitmap, idx), "bit %d should start unset", idx)
			bh.SetBit(bitmap, idx)
			assert.True(t, bh.IsSet(bitmap, idx), "bit %d should be set", idx)
		}
	})

	t.Run("out-of-range index is a no-op", func(t *testing.T) {
		bitmap := bh.NewBitmap(8)

		bh.SetBit(bitmap, -1)
		bh.SetBit(bitmap, 8)
		bh.SetBit(bitmap, 100)

		assert.Equal(t, 0, bh.PopCount(bitmap), "out-of-range SetBit must not flip any bit")
		assert.False(t, bh.IsSet(bitmap, -1))
		assert.False(t, bh.IsSet(bitmap, 8))
		assert.False(t, bh.IsSet(bitmap, 100))
	})

	t.Run("set is idempotent", func(t *testing.T) {
		bitmap := bh.NewBitmap(8)
		bh.SetBit(bitmap, 3)
		bh.SetBit(bitmap, 3)
		assert.Equal(t, 1, bh.PopCount(bitmap))
		assert.True(t, bh.IsSet(bitmap, 3))
	})

	t.Run("setting one bit does not affect neighbours", func(t *testing.T) {
		bitmap := bh.NewBitmap(16)
		bh.SetBit(bitmap, 4)
		for i := 0; i < 16; i++ {
			if i == 4 {
				continue
			}
			assert.False(t, bh.IsSet(bitmap, i), "bit %d must remain unset", i)
		}
	})
}

func TestPopCount(t *testing.T) {
	bh := NewBitmapHelper()

	specs := map[string]struct {
		bitmap   []byte
		expCount int
	}{
		"empty":              {bitmap: []byte{}, expCount: 0},
		"nil":                {bitmap: nil, expCount: 0},
		"all zeros":          {bitmap: []byte{0x00, 0x00}, expCount: 0},
		"all ones one byte":  {bitmap: []byte{0xFF}, expCount: 8},
		"all ones two bytes": {bitmap: []byte{0xFF, 0xFF}, expCount: 16},
		"single bit low":     {bitmap: []byte{0x01}, expCount: 1},
		"single bit high":    {bitmap: []byte{0x80}, expCount: 1},
		"mixed":              {bitmap: []byte{0x0F, 0xF0}, expCount: 8},
	}

	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, spec.expCount, bh.PopCount(spec.bitmap))
		})
	}
}

func TestOR(t *testing.T) {
	bh := NewBitmapHelper()

	t.Run("same length", func(t *testing.T) {
		dst := []byte{0x0F, 0x00}
		src := []byte{0xF0, 0xAA}
		bh.OR(dst, src)
		assert.Equal(t, []byte{0xFF, 0xAA}, dst)
	})

	t.Run("src shorter than dst leaves extra bytes untouched", func(t *testing.T) {
		dst := []byte{0x00, 0x00, 0xAA}
		src := []byte{0x0F}
		bh.OR(dst, src)
		assert.Equal(t, []byte{0x0F, 0x00, 0xAA}, dst)
	})

	t.Run("dst shorter than src ignores extra src bytes", func(t *testing.T) {
		dst := []byte{0x00}
		src := []byte{0x0F, 0xFF}
		bh.OR(dst, src)
		assert.Equal(t, []byte{0x0F}, dst)
	})

	t.Run("OR is commutative on overlap", func(t *testing.T) {
		a := []byte{0b11001100}
		b := []byte{0b10101010}
		expected := []byte{0b11101110}

		cp1 := append([]byte(nil), a...)
		bh.OR(cp1, b)
		cp2 := append([]byte(nil), b...)
		bh.OR(cp2, a)

		assert.Equal(t, expected, cp1)
		assert.Equal(t, expected, cp2)
	})
}

func TestAND(t *testing.T) {
	bh := NewBitmapHelper()

	t.Run("same length", func(t *testing.T) {
		dst := []byte{0xFF, 0xAA}
		src := []byte{0x0F, 0xF0}
		bh.AND(dst, src)
		assert.Equal(t, []byte{0x0F, 0xA0}, dst)
	})

	t.Run("src shorter than dst leaves extra bytes untouched", func(t *testing.T) {
		dst := []byte{0xFF, 0xFF, 0xAA}
		src := []byte{0x0F}
		bh.AND(dst, src)
		assert.Equal(t, []byte{0x0F, 0xFF, 0xAA}, dst)
	})

	t.Run("disjoint bits produce zero", func(t *testing.T) {
		dst := []byte{0xF0}
		src := []byte{0x0F}
		bh.AND(dst, src)
		assert.Equal(t, []byte{0x00}, dst)
	})
}

func TestCopy(t *testing.T) {
	bh := NewBitmapHelper()

	t.Run("nil input returns nil", func(t *testing.T) {
		assert.Nil(t, bh.Copy(nil))
	})

	t.Run("empty input returns empty non-nil slice", func(t *testing.T) {
		cp := bh.Copy([]byte{})
		require.NotNil(t, cp)
		assert.Len(t, cp, 0)
	})

	t.Run("copy is independent of original", func(t *testing.T) {
		original := []byte{0x0F, 0xF0}
		cp := bh.Copy(original)
		assert.Equal(t, original, cp)

		cp[0] = 0xAA
		assert.Equal(t, byte(0x0F), original[0], "mutating copy must not affect original")
		original[1] = 0xBB
		assert.Equal(t, byte(0xF0), cp[1], "mutating original must not affect copy")
	})
}

func TestClear(t *testing.T) {
	bh := NewBitmapHelper()

	t.Run("clears non-empty bitmap", func(t *testing.T) {
		bitmap := []byte{0xFF, 0xAA, 0x55}
		bh.Clear(bitmap)
		assert.Equal(t, []byte{0x00, 0x00, 0x00}, bitmap)
	})

	t.Run("clear on empty bitmap is a no-op", func(t *testing.T) {
		bitmap := []byte{}
		bh.Clear(bitmap)
		assert.Equal(t, []byte{}, bitmap)
	})

	t.Run("clear on nil is a no-op", func(t *testing.T) {
		var bitmap []byte
		bh.Clear(bitmap)
		assert.Nil(t, bitmap)
	})
}

func TestCountInRange(t *testing.T) {
	bh := NewBitmapHelper()

	// bitmap of 16 bits with bits 1, 3, 5, 8, 15 set
	bitmap := bh.NewBitmap(16)
	for _, idx := range []int{1, 3, 5, 8, 15} {
		bh.SetBit(bitmap, idx)
	}

	specs := map[string]struct {
		start, end int
		expCount   int
	}{
		"full range":                   {start: 0, end: 16, expCount: 5},
		"first byte only":              {start: 0, end: 8, expCount: 3},
		"second byte only":             {start: 8, end: 16, expCount: 2},
		"empty range":                  {start: 4, end: 4, expCount: 0},
		"inverted range is empty":      {start: 10, end: 4, expCount: 0},
		"end past bitmap length":       {start: 0, end: 1000, expCount: 5},
		"start past bitmap length":     {start: 200, end: 300, expCount: 0},
		"range excluding boundary bit": {start: 2, end: 5, expCount: 1},
		"range including boundary bit": {start: 1, end: 6, expCount: 3},
	}

	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, spec.expCount, bh.CountInRange(bitmap, spec.start, spec.end))
		})
	}
}

func TestBitmapRoundTrip(t *testing.T) {
	bh := NewBitmapHelper()

	indices := []int{0, 3, 7, 8, 15, 63, 99}
	bitmap := bh.NewBitmap(100)
	for _, idx := range indices {
		bh.SetBit(bitmap, idx)
	}

	assert.Equal(t, len(indices), bh.PopCount(bitmap))
	for _, idx := range indices {
		assert.True(t, bh.IsSet(bitmap, idx), "bit %d should be set", idx)
	}

	cp := bh.Copy(bitmap)
	bh.Clear(bitmap)
	assert.Equal(t, 0, bh.PopCount(bitmap))
	assert.Equal(t, len(indices), bh.PopCount(cp), "copy must be unaffected by clearing original")
}
