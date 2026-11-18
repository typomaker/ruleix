package ruleix_test

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/typomaker/ruleix"
)

func TestBatchOnlyPublicSearchMethods(t *testing.T) {
	kind := reflect.TypeOf((*ruleix.Index[int, int])(nil))
	for _, name := range []string{"Local", "SearchBatch"} {
		_, exists := kind.MethodByName(name)
		require.False(t, exists, "legacy method %s remains exported", name)
	}
	method, exists := kind.MethodByName("Search")
	require.True(t, exists)
	require.Equal(t, reflect.TypeOf(func(*ruleix.Index[int, int], []int, [][]int) {}), method.Type)
}
