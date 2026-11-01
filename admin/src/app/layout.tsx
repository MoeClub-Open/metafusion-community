import type { Metadata, Viewport } from "next";
import "@/shared/theme/globals.css";
import { Providers } from "@/components/Providers";
import { ThemeProvider } from "@/lib/themeContext";
import { DEFAULT_ACCENT, DEFAULT_TONE, DEFAULT_PATTERN, DEFAULT_BG_DARK } from "@/lib/theme.generated";
import { themeBootScript } from "@/shared/theme/boot.generated";

export const viewport: Viewport = { themeColor: DEFAULT_BG_DARK };

// 后台不进搜索引擎：它是运营界面，没有需要被发现的内容。
export const metadata: Metadata = {
  title: "Community Admin · MetaFusion",
  description: "MetaFusion 互动服务运营后台（板块、主题与帖子治理）",
  robots: { index: false, follow: false },
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh-CN" className="dark" data-theme-mode="dark" data-theme-accent={DEFAULT_ACCENT} data-theme-tone={DEFAULT_TONE} data-theme-pattern={DEFAULT_PATTERN} suppressHydrationWarning>
      <head><script dangerouslySetInnerHTML={{ __html: themeBootScript }} /></head>
      <body className="font-sans antialiased">
        <ThemeProvider>
        <Providers>{children}</Providers>
        </ThemeProvider>
      </body>
    </html>
  );
}
