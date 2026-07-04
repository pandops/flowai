import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Hello } from '../src/index.js';

describe('@flowai/ui placeholder', () => {
  it('renders a greeting', () => {
    render(<Hello name="Sisyphus" />);
    expect(screen.getByTestId('hello').textContent).toBe('Hello, Sisyphus!');
  });
});
