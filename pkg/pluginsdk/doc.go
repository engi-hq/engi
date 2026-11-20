// Package pluginsdk serves nodesdk.Node implementations as an Engi plugin
// speaking the Engi Plugin Protocol v1 (docs/15-plugins.md).
//
// A plugin is a program whose main function hands its nodes to Serve:
//
//	func main() {
//		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
//		defer stop()
//		info := pluginproto.Info{Name: "acme-text", Version: "1.0.0"}
//		if err := pluginsdk.Serve(ctx, info, reverseNode{}); err != nil {
//			fmt.Fprintln(os.Stderr, err)
//			os.Exit(1)
//		}
//	}
//
// Serve talks over stdin and stdout, so a stdio plugin must write nothing else
// to stdout; use stderr or Input.Logger for diagnostics. Handler serves the
// same nodes over HTTP for plugins deployed as services.
package pluginsdk
