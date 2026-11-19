/* EXPECTED: TLE - volatile counter, loop cannot be optimized away */
int main(void)
{
    for (volatile unsigned long i = 0;; i++)
        ;
}
