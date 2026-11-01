import React from "react";
import { captureException } from "../common/glitchtip";

class ErrorBoundary extends React.Component {
  state = { hasError: false, errorMessage: null };

  static getDerivedStateFromError(error) {
    // Update state so the next render will show the fallback UI.
    return { hasError: true, errorMessage: error?.toString() };
  }

  componentDidCatch(error, info) {
    console.log(error, info.componentStack);
    captureException(error, { source: "ErrorBoundary", componentStack: info.componentStack });
  }

  render() {
    if (this.state.hasError) {
      // You can render any custom fallback UI
      return <h1>Something went wrong: {this.state.errorMessage}</h1>;
    }

    return this.props.children;
  }
}

export default ErrorBoundary;
