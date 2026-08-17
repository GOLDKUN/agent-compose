package storeutil

import "fmt"

// ReportClose folds the result of closing a query cursor into the enclosing
// call's error, wrapped as "close <operation>".
//
// The close failure is only reported when the call is otherwise succeeding. An
// error already on its way out names the actual problem, and a cursor that then
// fails to close is usually a consequence of it rather than a second diagnosis.
//
// Reading the enclosing error after the body has run means taking a pointer to
// it, so the caller needs a named return. Passing the close result rather than
// the cursor keeps the literal rows.Close() at the call site, where
// sqlclosecheck can see it:
//
//	func (s *store) load(ctx context.Context) (_ []Record, err error) {
//		rows, err := s.db.QueryContext(ctx, query)
//		if err != nil {
//			return nil, fmt.Errorf("query records: %w", err)
//		}
//		defer func() { storeutil.ReportClose(rows.Close(), &err, "record page") }()
//
// A deferred close runs when its own function returns, which is not always
// early enough: SQLite refuses to commit while a cursor opened on the
// transaction is still open. What orders those two is draining the cursor in a
// function that returns before the commit, not the defer by itself.
func ReportClose(closeErr error, err *error, operation string) {
	ReportCloseWith(closeErr, err, operation, wrapCloseError)
}

// ReportCloseWith is ReportClose for stores whose callers classify errors by
// identity. wrap builds the reported error from the phrase ReportClose would
// have used ("close "+operation) and the close failure, so a store can keep its
// own sentinel in the chain instead of the plain fmt.Errorf shape.
func ReportCloseWith(closeErr error, err *error, operation string, wrap func(phrase string, closeErr error) error) {
	if closeErr == nil || *err != nil {
		return
	}
	*err = wrap("close "+operation, closeErr)
}

func wrapCloseError(phrase string, closeErr error) error {
	return fmt.Errorf("%s: %w", phrase, closeErr)
}
