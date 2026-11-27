import { render } from '@testing-library/preact';
import { describe, expect, it } from 'vitest';
import type { BranchStep } from '../stores/branch-progress';
import { BranchStepper } from './BranchStepper';

const stepper = (created: number) => {
  const step: BranchStep = {
    mode: 'notes',
    source: 1,
    started: 1_000,
    child: 2,
    created,
    action: {},
  };
  const noop = () => {};
  return render(<BranchStepper step={step} onSkip={noop} onOpen={noop} onRetry={noop} />);
};

describe('BranchStepper', () => {
  it('hides step times that would round to 0.0s', () => {
    const { container } = stepper(1_049);
    expect(container.querySelector('.branch-step-time')).toBeNull();
  });

  it('shows step times of at least 0.1s', () => {
    const { container } = stepper(1_050);
    expect(container.querySelector('.branch-step-time')?.textContent).toBe('0.1s');
  });
});
