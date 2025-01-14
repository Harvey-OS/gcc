/* { dg-do compile } */
/* { dg-options "-mavx512f -O2" } */
/* { dg-final { scan-assembler-times "kaddw\[ \\t\]+\[^\{\n\]*%k\[0-7\](?:\n|\[ \\t\]+#)" 1 } } */

#include <immintrin.h>

void
avx512f_test ()
{
  __mmask16 k = _kadd_mask16 (11, 12);
  asm volatile ("" : "+k" (k));
}
