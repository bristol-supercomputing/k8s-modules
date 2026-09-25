#include <stdio.h>

extern const char *fakefabric_variant(void);

int main(void) {
	printf("fakedep=%s\n", fakefabric_variant());
	return 0;
}
