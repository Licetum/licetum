/**
 * @jest-environment jsdom
 */
import { fireEvent, render, screen } from '@testing-library/react';
import type { ReactElement } from 'react';
import { I18nextProvider } from 'react-i18next';

import { ErrorBlock } from '@/components/kit';
import { createI18n } from '@/i18n';

// The interface reads its texts from the dictionaries, so the tests render with the real English ones.
function renderInEnglish(ui: ReactElement) {
  return render(<I18nextProvider i18n={createI18n('en')}>{ui}</I18nextProvider>);
}

// The shape of a failed call to the admin API: the status and the problem document of the backend.
const apiError = (detail: string) => ({ status: 424, data: { detail } });

describe('ErrorBlock', () => {
  it('shows that the data did not load, with the reason the API gives', () => {
    renderInEnglish(<ErrorBlock error={apiError('the source did not answer')} />);
    expect(screen.getByText('Couldn’t load the data')).toBeTruthy();
    expect(screen.getByText('the source did not answer')).toBeTruthy();
  });

  it('calls back when the person asks to try again', () => {
    const onRetry = jest.fn();
    renderInEnglish(<ErrorBlock error={apiError('boom')} onRetry={onRetry} />);

    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));

    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it('offers no retry when there is nothing to retry', () => {
    renderInEnglish(<ErrorBlock error={apiError('boom')} />);
    expect(screen.queryByRole('button', { name: 'Try again' })).toBeNull();
  });
});
