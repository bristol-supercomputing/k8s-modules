#include <stdlib.h>

extern const char *fakedep_variant(void);

/* arc4random is new in glibc 2.36, so this library requires GLIBC_2.36. */
const char *fakefabric_variant(void) {
	(void)arc4random();
	return fakedep_variant();
}
