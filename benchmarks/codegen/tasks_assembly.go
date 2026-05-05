package main

import "time"

type assemblySumPositiveTask struct{}

func (assemblySumPositiveTask) Name() string       { return "asm_sum_positive_perf" }
func (assemblySumPositiveTask) Language() string   { return "x86_64-assembly" }
func (assemblySumPositiveTask) Difficulty() string { return "medium-perf-correctness" }
func (assemblySumPositiveTask) Prompt() string {
	return `Write a complete x86-64 GNU assembler source file for Linux using AT&T syntax that defines exactly this exported function:

long sum_positive(long *xs, long n);

System V AMD64 ABI:
- xs is in %rdi
- n is in %rsi
- return value is in %rax

The function must return the sum of all positive values in xs[0:n]. Ignore zero and negative values. If n <= 0, return 0.

Requirements:
- Use only assembly; do not include C code.
- Export the symbol as sum_positive.
- Do not call libc or any external function.
- Must be correct for negative numbers, empty arrays, and mixed values.
- Prefer a simple fast loop over cleverness.`
}

func (assemblySumPositiveTask) Score(response string, timeout time.Duration) ScoreResult {
	return scoreAssembly(response, timeout, `
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/resource.h>
#include <time.h>

long sum_positive(long *xs, long n);

static void check(const char *name, long *xs, long n, long want) {
  long got = sum_positive(xs, n);
  if (got != want) {
    fprintf(stderr, "%s: got %ld want %ld\n", name, got, want);
    exit(1);
  }
}

static double elapsed_ms(struct timespec start, struct timespec end) {
  return (double)(end.tv_sec - start.tv_sec) * 1000.0 + (double)(end.tv_nsec - start.tv_nsec) / 1000000.0;
}

int main(void) {
  long empty[] = {1};
  long mixed[] = {-5, 0, 7, -2, 9, 1};
  long negatives[] = {-9, -8, -7};
  check("n<=0", empty, 0, 0);
  check("mixed", mixed, 6, 17);
  check("negative", negatives, 3, 0);

  const long n = 4096;
  long *xs = malloc(sizeof(long) * n);
  if (!xs) return 1;
  long want = 0;
  for (long i = 0; i < n; i++) {
    long v = (i % 11) - 5;
    xs[i] = v;
    if (v > 0) want += v;
  }
  check("bulk", xs, n, want);

  volatile long sink = 0;
  struct timespec a, b;
  clock_gettime(CLOCK_MONOTONIC, &a);
  for (int i = 0; i < 10000; i++) sink += sum_positive(xs, n);
  clock_gettime(CLOCK_MONOTONIC, &b);
  printf("BENCH_WARMUP_MS=%.3f\n", elapsed_ms(a, b));

  clock_gettime(CLOCK_MONOTONIC, &a);
  for (int i = 0; i < 200000; i++) sink += sum_positive(xs, n);
  clock_gettime(CLOCK_MONOTONIC, &b);
  if (sink == 42) puts("impossible");
  printf("BENCH_RUNTIME_MS=%.3f\n", elapsed_ms(a, b));
  struct rusage usage;
  getrusage(RUSAGE_SELF, &usage);
  printf("BENCH_MEMORY_KB=%ld\n", usage.ru_maxrss);
  free(xs);
  return 0;
}
`)
}
