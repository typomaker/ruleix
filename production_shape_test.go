package ruleix_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/typomaker/ruleix"
)

func TestProductionShapePlatformMatching(t *testing.T) {
	id := func(value byte) productionBenchmarkID {
		var result productionBenchmarkID
		result[len(result)-1] = value
		return result
	}
	constraints := []productionBenchmarkConstraint{
		{},
		{platform: &productionBenchmarkPlatform{name: "ios"}},
		{platform: &productionBenchmarkPlatform{
			name: "ios", version: &productionBenchmarkVersion{major: 1},
		}},
		{platform: &productionBenchmarkPlatform{
			name: "ios", version: &productionBenchmarkVersion{major: 2},
		}},
		{platform: &productionBenchmarkPlatform{
			name: "ios", version: &productionBenchmarkVersion{major: 2, minor: 1},
		}},
		{platform: &productionBenchmarkPlatform{name: "android"}},
	}
	ids := []productionBenchmarkID{id(1), id(2), id(3), id(4), id(5), id(6)}
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkSchema(),
	).Build(ruleix.Zip(constraints, ids))
	require.NoError(t, err)

	tests := []struct {
		name  string
		query productionBenchmarkConstraint
		want  []productionBenchmarkID
	}{
		{
			name: "missing name matches only the platform wildcard",
			want: []productionBenchmarkID{id(1)},
		},
		{
			name: "name with missing version includes the matching name version wildcard",
			query: productionBenchmarkConstraint{platform: &productionBenchmarkPlatform{
				name: "ios",
			}},
			want: []productionBenchmarkID{id(1), id(2)},
		},
		{
			name: "name and version include matching name and satisfied version constraints",
			query: productionBenchmarkConstraint{platform: &productionBenchmarkPlatform{
				name: "ios", version: &productionBenchmarkVersion{major: 2},
			}},
			want: []productionBenchmarkID{id(1), id(2), id(3), id(4)},
		},
		{
			name: "different name excludes constraints for other platforms",
			query: productionBenchmarkConstraint{platform: &productionBenchmarkPlatform{
				name: "android", version: &productionBenchmarkVersion{major: 100},
			}},
			want: []productionBenchmarkID{id(1), id(6)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var indexMatches []productionBenchmarkID
			index.Search(tt.query, &indexMatches)
			require.Equal(t, tt.want, indexMatches)

			var localMatches []productionBenchmarkID
			index.Local().Search(tt.query, &localMatches)
			require.Equal(t, tt.want, localMatches)
		})
	}
}
