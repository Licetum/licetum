import { theme as antdTheme, type ThemeConfig } from 'antd';

/** The visual system of the interface. The colours were checked for contrast (WCAG 2.2 AA). */
const FONT_SANS =
  '"Inter Variable", Inter, "IBM Plex Sans", system-ui, -apple-system, "Segoe UI", Roboto, "Noto Sans SC", sans-serif';
const FONT_MONO =
  '"JetBrains Mono", "IBM Plex Mono", ui-monospace, SFMono-Regular, Menlo, monospace';

export type Scheme = 'light' | 'dark';

const palette = {
  light: {
    bgLayout: '#F6F7F9',
    bgContainer: '#FFFFFF',
    border: '#E2E8F0',
    text: '#0F172A',
    textSecondary: '#475569',
    primary: '#3F4DD8',
    onPrimary: '#FFFFFF',
    success: '#0B6B3F',
    error: '#A8261B',
    warning: '#9A4B05',
    neutral: '#EEF1F5',
    successBg: '#E6F4EC',
    successBorder: '#B7DCC6',
    errorBg: '#FCEAE8',
    errorBorder: '#F1BDB7',
    warningBg: '#FDF1E1',
    warningBorder: '#F2D3A6',
    infoBg: '#ECEEFC',
    infoBorder: '#C5CBF4',
  },
  dark: {
    bgLayout: '#0B1020',
    bgContainer: '#121826',
    border: '#273044',
    text: '#E6EAF2',
    textSecondary: '#9AA6BD',
    primary: '#8C97FF',
    // White on #8C97FF is 2.6:1; the dark ground gives 7.1:1.
    onPrimary: '#0B1020',
    success: '#6FD3A1',
    error: '#FF8F85',
    warning: '#F5B76B',
    neutral: '#1A2233',
    successBg: '#10281D',
    successBorder: '#1F5039',
    errorBg: '#2E1512',
    errorBorder: '#5C2A25',
    warningBg: '#2B1E0C',
    warningBorder: '#5A3E17',
    infoBg: '#1A2040',
    infoBorder: '#33407A',
  },
} as const;

export function themeFor(scheme: Scheme): ThemeConfig {
  const c = palette[scheme];
  return {
    algorithm: scheme === 'dark' ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
    cssVar: { key: 'licetum' },
    hashed: false,
    token: {
      colorPrimary: c.primary,
      colorSuccess: c.success,
      colorError: c.error,
      colorWarning: c.warning,
      colorInfo: c.primary,
      colorSuccessBg: c.successBg,
      colorSuccessBorder: c.successBorder,
      colorErrorBg: c.errorBg,
      colorErrorBorder: c.errorBorder,
      colorWarningBg: c.warningBg,
      colorWarningBorder: c.warningBorder,
      colorInfoBg: c.infoBg,
      colorInfoBorder: c.infoBorder,
      colorText: c.text,
      colorTextSecondary: c.textSecondary,
      colorBgLayout: c.bgLayout,
      colorBgContainer: c.bgContainer,
      colorBorder: c.border,
      colorBorderSecondary: c.border,
      colorTextLightSolid: c.onPrimary,
      borderRadius: 6,
      borderRadiusLG: 8,
      fontFamily: FONT_SANS,
      fontFamilyCode: FONT_MONO,
      fontSize: 14,
      controlHeight: 36,
      wireframe: false,
    },
    components: {
      Layout: {
        headerBg: c.bgContainer,
        headerHeight: 56,
        headerPadding: '0 24px',
        bodyBg: c.bgLayout,
      },
      Table: {
        headerBg: c.neutral,
        headerColor: c.textSecondary,
        rowHoverBg: c.neutral,
        cellPaddingBlock: 12,
      },
      Card: { borderRadiusLG: 8 },
      Button: { primaryShadow: 'none', defaultShadow: 'none', fontWeight: 500 },
      Tag: { borderRadiusSM: 4 },
    },
  };
}
