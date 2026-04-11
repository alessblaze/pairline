import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { ThemeToggle } from '../ThemeToggle';

const mockToggleTheme = vi.fn();

vi.mock('../../hooks/useTheme', () => ({
  useTheme: () => ({
    theme: currentTheme,
    toggleTheme: mockToggleTheme,
  }),
}));

let currentTheme: 'light' | 'dark' = 'light';

describe('ThemeToggle', () => {
  beforeEach(() => {
    mockToggleTheme.mockClear();
    currentTheme = 'light';
  });

  it('renders the toggle button with correct aria label', () => {
    render(<ThemeToggle />);
    const button = screen.getByRole('button', { name: /toggle theme/i });
    expect(button).toBeInTheDocument();
  });

  it('calls toggleTheme on click', () => {
    render(<ThemeToggle />);
    const button = screen.getByRole('button', { name: /toggle theme/i });
    fireEvent.click(button);
    expect(mockToggleTheme).toHaveBeenCalledTimes(1);
  });

  it('renders moon icon in light theme (switch-to-dark)', () => {
    currentTheme = 'light';
    const { container } = render(<ThemeToggle />);
    // In light mode the component renders the moon SVG (path starts with M21.752...)
    const svgPaths = container.querySelectorAll('path');
    expect(svgPaths.length).toBeGreaterThan(0);
    // The moon path contains "M21.752"
    const moonPath = Array.from(svgPaths).find(p => p.getAttribute('d')?.includes('21.752'));
    expect(moonPath).toBeTruthy();
  });

  it('renders sun icon in dark theme (switch-to-light)', () => {
    currentTheme = 'dark';
    const { container } = render(<ThemeToggle />);
    const svgPaths = container.querySelectorAll('path');
    expect(svgPaths.length).toBeGreaterThan(0);
    // The sun path contains "M12 3v2.25"
    const sunPath = Array.from(svgPaths).find(p => p.getAttribute('d')?.includes('M12 3v2.25'));
    expect(sunPath).toBeTruthy();
  });
});
