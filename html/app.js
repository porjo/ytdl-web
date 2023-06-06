
var player = null;

var loc = window.location, ws_uri;
if (loc.protocol === "https:") {
	ws_uri = "wss:";
} else {
	ws_uri = "ws:";
}
ws_uri += "//" + loc.host;
var path = loc.pathname.replace(/\/$/, '');
ws_uri += path + "/websocket";

var progTimer = null;
var progLast = null;
var seekTimer = null;

$(function(){
	var ws = new WebSocket(ws_uri);

	const url = new URL(window.location);
	const searchParams = new URLSearchParams(url.search);
	var inputURL = searchParams.get('url');
	if (inputURL) {
		$("#url").val(inputURL);
	}

	$("#url").on('paste', function(e) {
		var data = e.originalEvent.clipboardData.getData('Text');
		searchParams.set('url', encodeURI(data));
		window.location.search = searchParams;
	});

	$("#go-button").click(function() {
		if (ws.readyState === 1) {
			$("#spinner").show();
			$("#input-form").hide();
			$("#progress-bar > span").css("width", "0%")
				.text("0%");
			var url = $("#url").val();
			var val = {URL: url};
			ws.send(JSON.stringify(val));
			$("#status").prepend("Requesting URL " + url + "\n");
			$(this).prop('disabled', true);
		} else {
			$("#status").prepend("socket not ready\n")
		}
	});

	$("#searchbox span").click(function() {
		$("#url").val('');
	});

	$("#recent_header .controls").click(function() {
		let urls = [];
		$(".recent_url.selected").each(function() {
			urls.push($(this).find(".stream_play").data("stream_url"));
			$(this).remove();
		});

		if( urls.length > 0 ) {
			var param = {delete_urls: urls};
			ws.send(JSON.stringify(param));
		}
	});


	/*
	ws.onopen = function() {
	};
	*/

	ws.onmessage = function (e)	{
		var msg = JSON.parse(e.data);
		if( 'Key' in msg ) {
			switch (msg.Key) {
				case 'error':
					$("#output").show();
					$("#spinner").hide();
					$("#status").prepend("Error: " + msg.Value + "\n");
					break;
				case 'unknown':
					$("#output").show();
					$("#status").prepend(msg.Value);
					break;
				case 'completed':
					$("#spinner").hide();
					$("#output").hide();
					clearTimeout(progTimer);
					break;
				case 'progress':
					$("#output").show();
					$("#spinner").hide();
					$("#progress-bar").show();
					progLast = Date.now();
					if(!progTimer) {
						progTimer = setInterval(() => {
							let now = Date.now();
							if( (now - progLast) > 4000) {
								$("#spinner").show();
								$("#progress-bar").hide();
							}
						},1000);
					}
					var pct = parseFloat(msg.Value.Pct);
					$("#progress-bar > span").css("width", pct + "%")
						.text(pct + "%");
					$("#eta").text( msg.Value.ETA );
					if( msg.Value.FileSize !== '' ) {
						$("#filesize").text( msg.Value.FileSize + " MB" );
					}
					break;
				case 'info':
					$("#output").show();
					$("#title").text( msg.Value.Title );
					var bytes = parseFloat(msg.Value.FileSize)
					$("#filesize").text( (bytes / 1024 / 1024).toFixed(2) + " MB" );
					break;
				case 'link_stream':
					if(!isPlaying()) {
						$("#playa").show();
						updatePlayer(encodeURI(msg.Value.DownloadURL), msg.Value.Title, msg.Value.Artist);
					}
					break;
				case 'recent':
					if( msg.Value.length == 0 ) {
						break;
					}
					$("#recent").show();
					$("#recent_urls").empty();
					for (let i=0; i< msg.Value.length; i++) {
						let artist = msg.Value[i].Artist;
						let title = msg.Value[i].Title;

						let $ru = $("<div>", {class: 'recent_url'});
						let $cont = $("<div>", {class: 'media_meta'});
						$cont.append($("<span>", {class: 'media_artist', text: artist}));
						$cont.append($("<span>", {class: 'media_title', text: title}));
						$ru.append($cont);
						$ru.click(function() {
							$(this).toggleClass("selected");
						});

						$playButton = $("<svg>", {version: "2.0"}).append( $("<use>", {href: "#play-btn"}) );
						let $streamPlay = $("<div>", {class: 'stream_play', html: $playButton});
						// 'refresh' play button content to allow SVG to display
						$streamPlay.html($streamPlay.html());
						$streamPlay.data("stream_url", msg.Value[i].URL);
						$streamPlay.data("artist", artist);
						$streamPlay.data("title", title);
						$streamPlay.click(streamPlayClick);
						$ru.append($streamPlay);
						$("#recent_urls").append($ru);
					}
					break;
			}
		}
	};

	function streamPlayClick() {
		$("#playa").show();
		let url = $(this).data("stream_url");
		let title = $(this).data("title");
		let artist = $(this).data("artist");
		updatePlayer(url, title, artist, true);
	}

	function isPlaying() {
		if(player && !player.audio.paused) {
			return true;
		}
		return false
	}

	function updatePlayer(url, title, artist, autoplay=false) {
		if( player === null ) {
			player = new Shikwasa.Player({
				container: () => document.querySelector('#playa'),
				audio: {
					title: title,
					artist: artist,
					src: url
				},
				speedOptions: [1.0, 1.1, 1.2],
				download: true
			});
		} else {
			player.update({
				title: title,
				artist: artist,
				src: url
			});
		}

		document.title = title + " - " + artist;

		player.on('loadedmetadata',() => {
			let obj = JSON.parse(localStorage.getItem(url))
			if(obj && obj.lastPlayTimeSec>0) {
				player.seek(obj.lastPlayTimeSec);
				if( obj.playbackRate ) {
					player.playbackRate = obj.playbackRate;
				}
			}

			if(seekTimer !== null ) {
				return
			}

			seekTimer = setInterval(() => {
				// cleanup storage
				for (let i = 0; i < localStorage.length; i++) {
					let name = localStorage.key(i);
					let o = JSON.parse(localStorage.getItem(name));

					// remove if older than 3 days
					if(o && (new Date().getTime() - o.timestamp) > 259200 ) {
						localStorage.removeItem(name);
					}
				}

				// store latest play time
				let o = {lastPlayTimeSec: player.currentTime, timestamp: new Date().getTime(), playbackRate: player.playbackRate}
				localStorage.setItem(url, JSON.stringify(o));
			},2000);
		});

		if(autoplay) {
			player.play();
		}

		// Add space for player at bottom of page
		$("body").css("padding-bottom", $(".shk-player").css("height"));
	}

	ws.onclose = function()	{
		$("#status").prepend("Connection closed\n");
		console.log("Connection closed");
		$("#ws-status-light").toggleClass("on off");
	};

	ws.onopen = function(e) {
		$("#status").prepend("Connection opened\n");
		console.log("Connection opened");
		$("#ws-status-light").toggleClass("off on");
	}

	$("#status").click(function() {
		$(this).toggleClass("expand");
	});

});
