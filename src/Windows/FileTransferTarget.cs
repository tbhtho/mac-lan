namespace MacDow.Windows;

// This capability comes only from the already paired, certificate-pinned control connection.
internal sealed record TransferTarget(Uri Endpoint,string Fingerprint,string Token,CancellationToken SessionEnded);
