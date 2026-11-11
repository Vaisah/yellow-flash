package platform

// MarkFromInternet marks a file as downloaded from the internet, as a
// browser marks its downloads: on Windows the Mark of the Web (the
// Zone.Identifier stream, which SmartScreen and Office check), on Linux
// the freedesktop extended attributes user.xdg.origin.url and
// user.xdg.referrer.url (file managers show them). Elsewhere, or where the
// file system can't hold it, nothing. from and referrer may be "".
func MarkFromInternet(path, from, referrer string) { markFromInternet(path, from, referrer) }
