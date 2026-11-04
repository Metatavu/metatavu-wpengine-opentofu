<?php
/**
 * Only for the end-to-end tests, where wp-env differs from a WP Engine install
 * (the same as metatavu-wordpress-iac's tests/e2e/wp-env-shims.php):
 *
 * - wp-env checks WordPress out with git, which WordPress's automatic updater
 *   takes for a development copy and doesn't update.
 * - Inside the container, the site's own URL (http://localhost:<port>) isn't
 *   listening, so the updater's loopback check for fatal errors would fail and
 *   it would restore every update. Requests to the site go to port 80 instead,
 *   with the site's host in the Host header.
 *
 */

add_filter( 'automatic_updates_is_vcs_checkout', '__return_false' );

add_filter(
	'pre_http_request',
	function ( $pre, $args, $url ) {
		$home = untrailingslashit( home_url() );
		if ( false !== $pre || ! str_starts_with( $url, $home ) ) {
			return $pre;
		}
		$args['headers']         = (array) ( $args['headers'] ?? array() );
		$args['headers']['Host'] = (string) wp_parse_url( $home, PHP_URL_HOST ) . ':' . (string) wp_parse_url( $home, PHP_URL_PORT );
		return wp_remote_request( 'http://localhost' . substr( $url, strlen( $home ) ), $args );
	},
	10,
	3
);
