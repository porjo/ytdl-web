
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
				case 'link':
					$("#output").show();
					clearTimeout(progTimer);
					$("#spinner").hide();
					var $link = $("<a>")
						.attr("href", encodeURI(msg.Value.DownloadURL))
						.attr("download", "")
						.text(msg.Value.DownloadURL);
					$("#links").append($link);
					$("#links").append("<br>");
					$("#progress-bar > span").css("width", "100%")
						.text("100%");
					$("#go-button").prop('disabled', false);
					break;
				case 'link_stream':
					$("#playa").show();
					updatePlayer(encodeURI(msg.Value.DownloadURL), msg.Value.Title, msg.Value.Artist);
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
						$ru.append($("<span>", {text: artist}));
						$ru.append($("<span>", {text: title}));

						$playButton = $("#play_button_hidden svg").prop('outerHTML');
						let $streamPlay = $("<div>", {class: 'stream_play', html: $playButton});
						$streamPlay.data("stream_url", msg.Value[i].URL);
						$streamPlay.data("artist", artist);
						$streamPlay.data("title", title);
						$streamPlay.click(streamPlayClick);
						$ru.append($link);
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

	function updatePlayer(url, title, artist, autoplay=false) {
		if( player === null ) {
			player = new Shikwasa.Player({
				container: () => document.querySelector('#playa'),
				audio: {
					title: title,
					artist: artist,
					src: url
				},
				/*
				fixed: {
					type: 'fixed',
					position: 'bottom',
				},
				*/
				speedOptions: [1.0, 1.1, 1.2],
			});
		} else {
			//if( player.audio.paused ) {
				player.update({
					title: title,
					artist: artist,
					src: encodeURI(url)
				});
			//}
		}

		document.title = title + " -  " + artist;

		if(autoplay) {
			player.play();
		}
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
