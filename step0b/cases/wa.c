/* EXPECTED: WA - input "2 3" prints 6 (off by one) */
#include <stdio.h>

int main(void)
{
    int a, b;
    if (scanf("%d %d", &a, &b) != 2)
        return 1;
    printf("%d\n", a + b + 1);
    return 0;
}
