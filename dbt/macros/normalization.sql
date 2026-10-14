{% macro release_marker() -%}
  {%- set release = var('release_id') -%}
  {%- set inputs = var('input_manifest_sha256') -%}
  {%- if release | length != 36 or inputs | length != 64 -%}
    {{ exceptions.raise_compiler_error('A release UUID and frozen input manifest SHA-256 are required.') }}
  {%- endif -%}
-- release_id={{ release }} input_manifest_sha256={{ inputs }}
{%- endmacro %}

{% macro release_id() -%}
'{{ var("release_id") }}'::uuid
{%- endmacro %}

{% macro integer_value(expression) -%}
case when replace(btrim({{ expression }}), ',', '') ~ '^[0-9]+$'
then replace(btrim({{ expression }}), ',', '')::bigint else null end
{%- endmacro %}

{% macro seconds_value(expression) -%}
case when replace(btrim({{ expression }}), ',', '') ~ '^[0-9]+(\.[0-9]+)?$'
then round(replace(btrim({{ expression }}), ',', '')::numeric * 60)::bigint else null end
{%- endmacro %}

{% macro complete_sum(column) -%}
case when count({{ column }}) = count(*) then sum({{ column }}) else null end
{%- endmacro %}

{% macro rates(team=false) -%}
pts::numeric / nullif(games_played, 0) as pts_per_game,
reb::numeric / nullif(games_played, 0) as reb_per_game,
ast::numeric / nullif(games_played, 0) as ast_per_game,
pts::numeric * 2160 / nullif(minutes_seconds, 0) as pts_per36,
reb::numeric * 2160 / nullif(minutes_seconds, 0) as reb_per36,
ast::numeric * 2160 / nullif(minutes_seconds, 0) as ast_per36,
fg::numeric / nullif(fga, 0) as fg_pct,
fg3::numeric / nullif(fg3a, 0) as fg3_pct,
ft::numeric / nullif(fta, 0) as ft_pct,
(fg::numeric + 0.5 * fg3) / nullif(fga, 0) as efg_pct,
pts::numeric / nullif(2 * (fga::numeric + 0.44 * fta), 0) as ts_pct
{% if team %},
wins::numeric / nullif(games_played, 0) as win_pct,
pts::numeric / nullif(games_played, 0) as points_for_per_game,
opponent_pts::numeric / nullif(games_played, 0) as points_against_per_game,
(pts::numeric - opponent_pts) / nullif(games_played, 0) as point_differential_per_game
{% endif %}
{%- endmacro %}

{% test unique_key(model, columns) %}
select {{ columns | join(', ') }}, count(*) as duplicate_count
from {{ model }}
group by {{ columns | join(', ') }} having count(*) > 1
{% endtest %}
