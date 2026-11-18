/* EXPECTED: RE - null pointer write, dies by signal */
int main(void)
{
    int *p = 0;
    *p = 1;
    return 0;
}
