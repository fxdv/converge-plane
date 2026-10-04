import { GeistMono } from 'geist/font/mono';
import { GeistSans } from 'geist/font/sans';
import Document, {
  Head,
  Html,
  Main,
  NextScript,
  type DocumentContext,
  type DocumentInitialProps,
} from 'next/document';

type Props = DocumentInitialProps & { nonce?: string };

export default function ConvergeDocument({ nonce }: Props) {
  return (
    <Html lang="en" className={`${GeistMono.variable} ${GeistSans.variable}`}>
      <Head nonce={nonce} />
      <body className="font-sans">
        <Main />
        <NextScript nonce={nonce} />
      </body>
    </Html>
  );
}

ConvergeDocument.getInitialProps = async (
  ctx: DocumentContext,
): Promise<Props> => {
  const initialProps = await Document.getInitialProps(ctx);
  const raw = ctx.req?.headers['x-nonce'];
  const nonce = typeof raw === 'string' ? raw : undefined;
  return { ...initialProps, nonce };
};
