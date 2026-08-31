import { Button } from '@converge/ui/components/button';
import Error from 'next/error';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const CustomErrorComponent = (props: any) => {
  const reload = () => {
    window.location.reload();
  };

  return (
    <div className="h-[100vh] w-[100vw] flex justify-center items-center flex-col">
      <h1>
        {props.statusCode ? `Error ${props.statusCode}` : 'An error occurred'}
      </h1>
      <p>
        {props.statusCode === 404
          ? 'Page not found'
          : 'Something went wrong on our end. Please try again later.'}
      </p>
      <Button variant="secondary" className="mt-2" onClick={reload}>
        Reload page
      </Button>
    </div>
  );
};

// eslint-disable-next-line @typescript-eslint/no-explicit-any
CustomErrorComponent.getInitialProps = (contextData: any) =>
  Error.getInitialProps(contextData);

export default CustomErrorComponent;
