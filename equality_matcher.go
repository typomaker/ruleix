package ruleix

import "github.com/RoaringBitmap/roaring/v2"

func addEqualityMatches[K comparable](
	wildcard *roaring.Bitmap,
	values *equalityIndex[K],
	key optionalValue[K],
	dst *roaring.Bitmap,
) {
	dst.Or(wildcard)
	if key.ok {
		if set := values.get(key.value); set != nil {
			set.addTo(dst)
		}
	}
}
